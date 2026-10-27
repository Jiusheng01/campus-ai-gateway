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
//
// It is a single Consume call rather than a "may I?" / "record that I did"
// pair. Two calls would leave a window between them in which any number of
// concurrent requests could all be told yes; see the limiter for the full
// reasoning.
type registrationLimiter interface {
	ConsumeRegistration(ctx context.Context, ip string) (allowed bool, retryAfter time.Duration, err error)
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

	// Membership is joined before the session is opened, so the first page the
	// new Account loads already reflects reality. Assigning it afterwards
	// would let the personal console's very first fetch — which happens
	// immediately after sign-in — read "no Tenant assigned" and cache that
	// answer in the page the user is looking at.
	//
	// A failure here is not allowed to undo the registration: the account and
	// its password are already committed, and the operator can still assign
	// the Tenant by hand. It is logged loudly because the deployment was
	// configured to do this automatically and silently not doing it is exactly
	// the state this setting exists to prevent.
	if a.registrationTenantID != "" {
		assigned, err := a.assignUserTenant(ctx, user.ID, a.registrationTenantID)
		if err != nil {
			log.Printf("registration: account %s created but joining Tenant %s failed, an operator must assign it: %v",
				user.Username, a.registrationTenantID, err)
		} else {
			user = assigned
		}
	}

	tokens, err := a.openSession(ctx, user, ip, userAgent, loginMethodRegister)
	if err != nil {
		// The account exists; only the automatic sign-in failed. A generic
		// internal error here would tell the user their registration failed,
		// and their retry would answer "username taken" with nothing to
		// explain it. The dedicated sentinel keeps the partial success
		// recoverable: the user logs in with the password they just chose.
		// Deleting the account instead would be the wrong trade — the
		// credentials are verified and the failure is transient.
		log.Printf("registration: account %s created but the session could not be opened: %v", user.Username, err)
		return model.AdminUser{}, adminTokens{}, ErrAdminAccountCreatedLoginFailed
	}
	a.store.TouchAdminUserLogin(ctx, user.ID)
	return user, tokens, nil
}

// consumeRegistrationBudget charges one attempt against the caller's address.
//
// It is called once per attempt, after the cheap validation (username shape,
// display name length, password policy) and after the setup and
// registration-enabled gates, and before the account is created. That ordering
// is what makes a duplicate username cost budget: the request is otherwise
// well-formed, so an attacker cannot use a name they know is taken to probe the
// endpoint for free.
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
	allowed, retryAfter, err := a.registration.ConsumeRegistration(ctx, ip)
	if err != nil {
		log.Printf("registration: abuse counter unavailable, refusing the attempt: %v", err)
		return ErrAdminRegistrationUnavailable
	}
	if !allowed {
		log.Printf("registration: rate limited, retry after %s", retryAfter)
		return ErrAdminRegistrationRateLimited
	}
	return nil
}

// --- POST /auth/register ---------------------------------------------------

// registrationAuditAction names the audit row a failed registration writes.
//
// The partial success — account created, automatic sign-in not — has its own
// action, so the trail can tell three outcomes apart: nothing happened, the
// account exists but nobody is signed in, and the full success that is audited
// by the caller as account_registered.
func registrationAuditAction(err error) string {
	switch {
	case errors.Is(err, ErrAdminRegistrationRateLimited):
		return "registration_rate_limited"
	case errors.Is(err, ErrAdminRegistrationBeforeSetup):
		return "account_register_before_setup"
	case errors.Is(err, ErrAdminUsernameTaken):
		return "account_register_username_taken"
	case errors.Is(err, ErrAdminAccountCreatedLoginFailed):
		return "account_created_login_failed"
	default:
		return "account_register_failed"
	}
}

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
	if err := decodeAdminAuthBody(w, r, &in); err != nil {
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
		action := registrationAuditAction(err)
		s.auditAdminAuth(r, action, statusForAdminError(err), strings.TrimSpace(in.Username))
		if errors.Is(err, ErrAdminAccountCreatedLoginFailed) {
			s.recordSecurityEvent(r, model.SecurityEvent{
				RequestID: reqID,
				EventType: "account_created_login_failed",
				ClientIP:  ip,
				Detail:    "username=" + strings.TrimSpace(in.Username) + " account_created=true session_created=false",
			})
		}
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
