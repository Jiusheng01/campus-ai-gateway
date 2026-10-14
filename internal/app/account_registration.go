package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

// registrationLimiter throttles public account creation. It is a separate
// interface from adminLoginLimiter because the two have opposite semantics:
// login counters advance on failure, registration counters advance on every
// attempt.
type registrationLimiter interface {
	AllowRegistration(ctx context.Context, ip string) (bool, time.Duration, error)
	RecordRegistration(ctx context.Context, ip string) error
}

// register creates a public member account and signs it in.
//
// The role list is not a parameter. Public registration always produces
// ["member"], and keeping that decision inside the service — rather than at the
// HTTP boundary — means there is no argument to tamper with and no code path
// that could be persuaded to create an administrator.
//
// The account is signed in immediately, through the same session machinery as a
// password login. Making the user retype the password they just chose would be
// a worse experience for no security benefit.
func (a *adminAuthService) register(ctx context.Context, username, displayName, password, ip, userAgent string) (model.AdminUser, adminTokens, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)

	if username == "" || utf8.RuneCountInString(username) > 64 {
		return model.AdminUser{}, adminTokens{}, ErrAdminBadRequest
	}
	if utf8.RuneCountInString(displayName) > 128 {
		return model.AdminUser{}, adminTokens{}, ErrAdminBadRequest
	}
	if err := validateAdminPassword(username, password); err != nil {
		return model.AdminUser{}, adminTokens{}, err
	}

	// Setup owns the window before the first super administrator exists. If a
	// registration slipped through first, the deployment would be left with a
	// member account and no administrator — and the setup path is gated on
	// "no super_admin", so it would stay open, but the operator would have no
	// reason to think anything was wrong.
	needsSetup, err := a.needsSetup(ctx)
	if err != nil {
		return model.AdminUser{}, adminTokens{}, ErrAdminInternal
	}
	if needsSetup {
		return model.AdminUser{}, adminTokens{}, ErrAdminRegistrationBeforeSetup
	}

	if err := a.consumeRegistrationBudget(ctx, ip); err != nil {
		return model.AdminUser{}, adminTokens{}, err
	}

	hash, err := security.HashPassword(password)
	if err != nil {
		return model.AdminUser{}, adminTokens{}, ErrAdminInternal
	}

	user, err := a.store.CreateUser(ctx, username, displayName, hash, publicRegistrationRoles)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrAdminUsernameTaken):
			return model.AdminUser{}, adminTokens{}, ErrAdminUsernameTaken
		case errors.Is(err, storage.ErrAdminUserNotFound):
			return model.AdminUser{}, adminTokens{}, ErrAdminInternal
		default:
			return model.AdminUser{}, adminTokens{}, ErrAdminInternal
		}
	}

	tokens, err := a.openSession(ctx, user, ip, userAgent, loginMethodRegister)
	if err != nil {
		return model.AdminUser{}, adminTokens{}, err
	}
	a.store.TouchAdminUserLogin(ctx, user.ID)
	return user, tokens, nil
}

// consumeRegistrationBudget charges one attempt against the caller's address.
//
// Unlike login throttling this fails closed. The counter is the only thing
// standing between the internet and unlimited account creation, so treating a
// cache outage as "allow everything" would remove the protection exactly when
// the system is least able to absorb abuse. A caller who is refused gets 503
// and can try again once the counter is reachable.
func (a *adminAuthService) consumeRegistrationBudget(ctx context.Context, ip string) error {
	if a.registration == nil {
		return nil
	}
	allowed, _, err := a.registration.AllowRegistration(ctx, ip)
	if err != nil {
		log.Printf("registration: abuse counter unavailable, refusing the attempt: %v", err)
		return ErrAdminRegistrationUnavailable
	}
	if !allowed {
		return ErrAdminRegistrationRateLimited
	}
	if err := a.registration.RecordRegistration(ctx, ip); err != nil {
		log.Printf("registration: could not record the attempt, refusing it: %v", err)
		return ErrAdminRegistrationUnavailable
	}
	return nil
}

// --- POST /auth/register ---------------------------------------------------

type accountRegisterRequest struct {
	Username        string `json:"username"`
	DisplayName     string `json:"display_name"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirm_password"`
}

// handleAccountRegister serves public account creation.
//
// The body is decoded with DisallowUnknownFields, so a request that tries to
// carry "roles", "status" or "is_admin" is rejected outright rather than
// quietly ignored. Silently dropping the field would also be safe, but refusing
// it makes the attempt visible in the audit trail instead of invisible.
func (s *Server) handleAccountRegister(w http.ResponseWriter, r *http.Request) {
	writeAuthNoStore(w)
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	// Registration belongs to the local account system. In the token and
	// trusted-header modes the endpoint does not exist, rather than existing
	// and refusing: those deployments have no local accounts to register.
	if !s.cfg.SessionAuthEnabled() || s.adminAuth == nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "Not found", reqID)
		return
	}
	if !s.cfg.PublicRegistrationEnabled {
		s.auditAdminAuth(r, "account_register_rejected", http.StatusForbidden, "")
		writeAdminError(w, reqID, ErrAdminRegistrationDisabled)
		return
	}

	var in accountRegisterRequest
	if err := decodeAdminAuthBody(r, &in); err != nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}
	if in.Password != in.ConfirmPassword {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}

	ip := s.clientIPFrom(r)
	user, tokens, err := s.adminAuth.register(r.Context(), in.Username, in.DisplayName, in.Password, ip, r.UserAgent())
	if err != nil {
		action := "account_register_failed"
		switch {
		case errors.Is(err, ErrAdminRegistrationRateLimited):
			action = "registration_rate_limited"
		case errors.Is(err, ErrAdminRegistrationBeforeSetup):
			action = "account_register_before_setup"
		case errors.Is(err, ErrAdminUsernameTaken):
			action = "account_register_username_taken"
		}
		s.auditAdminAuth(r, action, statusForAdminError(err), strings.TrimSpace(in.Username))
		writeAdminError(w, reqID, err)
		return
	}

	s.auditAdminAuth(r, "account_registered", http.StatusCreated, user.Username)
	// The security-event row carries the identity, never the credential: a
	// registration is worth being able to trace, but the password and the
	// freshly minted tokens have no business in a log.
	s.recordSecurityEvent(r, model.SecurityEvent{
		RequestID: reqID,
		EventType: "account_registered",
		ClientIP:  ip,
		Detail:    "username=" + user.Username + " user_id=" + user.ID,
	})
	s.issueRefreshCookie(w, tokens)
	writeJSON(w, http.StatusCreated, loginResponseFrom(tokens))
}
