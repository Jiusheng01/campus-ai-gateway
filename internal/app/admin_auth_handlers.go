package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// adminAuthBodyLimit caps the request body of the public auth endpoints. They
// carry a few short strings; anything larger is an attack or a bug.
const adminAuthBodyLimit = 4 << 10

// --- response shapes -------------------------------------------------------

// adminAuthConfigResponse describes the login system to an unauthenticated
// caller. It deliberately contains no credential concept: the operator never
// sees an ADMIN_TOKEN, an initialisation token, or any other token in the UI.
type adminAuthConfigResponse struct {
	AuthMode          string `json:"auth_mode"`
	LoginEnabled      bool   `json:"login_enabled"`
	NeedsSetup        bool   `json:"needs_setup"`
	SetupLocalOnly    bool   `json:"setup_local_only"`
	SetupAllowedHere  bool   `json:"setup_allowed_here"`
	MinPasswordLength int    `json:"min_password_length"`
	AccessTTLSeconds  int64  `json:"access_ttl_seconds"`
}

// adminAccountView is the safe projection of an account. The password hash is
// never serialised.
type adminAccountView struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
}

// adminSessionView is the safe projection of a session.
type adminSessionView struct {
	ID           string `json:"id"`
	Current      bool   `json:"current"`
	LoginMethod  string `json:"login_method"`
	IP           string `json:"ip"`
	UserAgent    string `json:"user_agent"`
	CreatedAt    string `json:"created_at"`
	LastActiveAt string `json:"last_active_at"`
	ExpiresAt    string `json:"expires_at"`
}

type adminLoginResponse struct {
	AccessToken string           `json:"access_token"`
	ExpiresAt   string           `json:"expires_at"`
	User        adminAccountView `json:"user"`
}

// writeAuthNoStore marks an authentication response as uncacheable.
//
// These responses carry an access token or describe account state, so neither a
// shared cache nor the browser's back/forward cache may replay them.
func writeAuthNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

// --- GET /admin/auth-config ------------------------------------------------

func (s *Server) handleAdminAuthConfig(w http.ResponseWriter, r *http.Request) {
	writeAuthNoStore(w)
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	out := adminAuthConfigResponse{
		AuthMode:          strings.ToLower(strings.TrimSpace(s.cfg.AdminAuthMode)),
		LoginEnabled:      s.cfg.SessionAuthEnabled(),
		SetupLocalOnly:    s.cfg.AdminSetupLocalOnly,
		SetupAllowedHere:  !s.cfg.AdminSetupLocalOnly || s.isLocalSetupRequest(r),
		MinPasswordLength: MinAdminPasswordLength,
		AccessTTLSeconds:  int64(s.cfg.AdminAccessTTL / time.Second),
	}
	if s.cfg.SessionAuthEnabled() && s.adminAuth != nil {
		needsSetup, err := s.adminAuth.needsSetup(r.Context())
		if err != nil {
			log.Printf("admin auth-config: cannot count administrators: %v", err)
			writeAdminError(w, reqID, ErrAdminInternal)
			return
		}
		out.NeedsSetup = needsSetup
	}
	writeJSON(w, http.StatusOK, out)
}

// --- POST /admin/setup -----------------------------------------------------

type adminSetupRequest struct {
	Username        string `json:"username"`
	DisplayName     string `json:"display_name"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirm_password"`
}

func (s *Server) handleAdminSetup(w http.ResponseWriter, r *http.Request) {
	writeAuthNoStore(w)
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	if !s.cfg.SessionAuthEnabled() || s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return
	}

	needsSetup, err := s.adminAuth.needsSetup(r.Context())
	if err != nil {
		log.Printf("admin setup: cannot count administrators: %v", err)
		writeAdminError(w, reqID, ErrAdminInternal)
		return
	}
	if !needsSetup {
		s.auditAdminAuth(r, "admin_setup_rejected", http.StatusForbidden, "administrator already exists")
		writeAdminError(w, reqID, ErrAdminSetupNotAllowed)
		return
	}
	// First-run setup is restricted to the machine itself. A remote caller is
	// told the setup is pending but not permitted here, rather than getting a
	// generic 403 that would be indistinguishable from a permissions problem.
	if s.cfg.AdminSetupLocalOnly && !s.isLocalSetupRequest(r) {
		s.auditAdminAuth(r, "admin_setup_remote_rejected", http.StatusForbidden, "setup attempted from "+s.clientIPFrom(r))
		writeAdminError(w, reqID, ErrAdminSetupRemoteForbidden)
		return
	}

	var in adminSetupRequest
	if err := decodeAdminAuthBody(r, &in); err != nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}
	if in.Password != in.ConfirmPassword {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}

	user, err := s.adminAuth.setup(r.Context(), in.Username, in.DisplayName, in.Password)
	if err != nil {
		s.auditAdminAuth(r, "admin_setup_failed", statusForAdminError(err), in.Username)
		writeAdminError(w, reqID, err)
		return
	}

	// Sign the operator straight in; there is no reason to make them retype the
	// password they just chose.
	tokens, err := s.adminAuth.login(r.Context(), user.Username, in.Password, s.clientIPFrom(r), r.UserAgent())
	if err != nil {
		s.auditAdminAuth(r, "admin_setup_failed", statusForAdminError(err), user.Username)
		writeAdminError(w, reqID, err)
		return
	}
	s.auditAdminAuth(r, "admin_setup", http.StatusCreated, user.Username)
	s.issueRefreshCookie(w, tokens)
	writeJSON(w, http.StatusCreated, loginResponseFrom(tokens))
}

// --- POST /admin/login -----------------------------------------------------

type adminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	writeAuthNoStore(w)
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	if !s.cfg.SessionAuthEnabled() || s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return
	}

	var in adminLoginRequest
	if err := decodeAdminAuthBody(r, &in); err != nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}

	ip := s.clientIPFrom(r)
	tokens, err := s.adminAuth.login(r.Context(), in.Username, in.Password, ip, r.UserAgent())
	if err != nil {
		action := "admin_login_failed"
		if errors.Is(err, ErrAdminRateLimited) {
			action = "admin_login_throttled"
		}
		s.auditAdminAuth(r, action, statusForAdminError(err), strings.TrimSpace(in.Username))
		writeAdminError(w, reqID, err)
		return
	}

	s.auditAdminAuth(r, "admin_login", http.StatusOK, tokens.User.Username)
	s.issueRefreshCookie(w, tokens)
	writeJSON(w, http.StatusOK, loginResponseFrom(tokens))
}

// --- POST /admin/auth/refresh ---------------------------------------------

func (s *Server) handleAdminRefresh(w http.ResponseWriter, r *http.Request) {
	writeAuthNoStore(w)
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	if !s.cfg.SessionAuthEnabled() || s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return
	}
	if err := s.checkSameOrigin(r); err != nil {
		writeAdminError(w, reqID, err)
		return
	}

	raw, err := readAdminRefreshCookie(r)
	if err != nil {
		s.clearAdminRefreshCookie(w)
		writeAdminError(w, reqID, ErrAdminUnauthenticated)
		return
	}

	tokens, err := s.adminAuth.refresh(r.Context(), raw, s.clientIPFrom(r), r.UserAgent())
	if err != nil {
		// Only a genuine failure invalidates the cookie. A benign concurrent
		// refresh returns success above, so a second tab can never clear the
		// rotation the first tab just received.
		if errors.Is(err, ErrAdminRefreshReplay) {
			s.auditAdminAuth(r, "admin_refresh_replay_detected", http.StatusUnauthorized, "")
		}
		s.clearAdminRefreshCookie(w)
		writeAdminError(w, reqID, err)
		return
	}
	s.issueRefreshCookie(w, tokens)
	writeJSON(w, http.StatusOK, loginResponseFrom(tokens))
}

// --- POST /admin/logout and POST /admin/auth/logout ------------------------

// handleAdminLogout revokes the session behind the refresh cookie.
//
// The cookie Path is scoped to /admin/auth, so a browser only attaches it to
// /admin/auth/logout. /admin/logout exists as an alias for API clients, which
// are not subject to cookie path rules; from a browser it can only clear the
// cookie, which is why the UI calls the /admin/auth/logout form.
func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	writeAuthNoStore(w)
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	if err := s.checkSameOrigin(r); err != nil {
		writeAdminError(w, reqID, err)
		return
	}

	if raw, err := readAdminRefreshCookie(r); err == nil && s.adminAuth != nil {
		if err := s.adminAuth.logout(r.Context(), raw); err != nil {
			log.Printf("admin logout: %v", err)
		}
		s.auditAdminAuth(r, "admin_logout", http.StatusOK, "")
	}
	s.clearAdminRefreshCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- POST /admin/password --------------------------------------------------

type adminPasswordChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleAdminPasswordChange(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	principal, ok := adminPrincipalFrom(r.Context())
	if !ok || principal.UserID == "" {
		writeAdminError(w, reqID, ErrAdminForbidden)
		return
	}

	var in adminPasswordChangeRequest
	if err := decodeAdminAuthBody(r, &in); err != nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}

	if err := s.adminAuth.changePassword(r.Context(), principal.UserID, in.CurrentPassword, in.NewPassword); err != nil {
		action := "admin_password_change_failed"
		if errors.Is(err, ErrAdminCredentialsInvalid) {
			action = "admin_password_change_rejected"
		}
		s.auditAdminAuth(r, action, statusForAdminError(err), principal.Username)
		writeAdminError(w, reqID, err)
		return
	}

	// Every session was revoked, including this one, so the cookie must go too.
	s.clearAdminRefreshCookie(w)
	s.auditAdminAuth(r, "admin_password_change", http.StatusOK, principal.Username)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "Password updated. Please sign in again with the new password.",
	})
}

// --- GET /admin/sessions ---------------------------------------------------

func (s *Server) handleAdminSessions(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	principal, ok := adminPrincipalFrom(r.Context())
	if !ok || principal.UserID == "" {
		writeAdminError(w, reqID, ErrAdminForbidden)
		return
	}

	sessions, err := s.adminAuth.listSessions(r.Context(), principal.UserID)
	if err != nil {
		writeAdminError(w, reqID, err)
		return
	}
	views := make([]adminSessionView, 0, len(sessions))
	for _, sess := range sessions {
		views = append(views, adminSessionView{
			ID:           sess.SID,
			Current:      sess.SID == principal.SessionID,
			LoginMethod:  sess.LoginMethod,
			IP:           sess.IP,
			UserAgent:    sess.UserAgent,
			CreatedAt:    sess.CreatedAt.UTC().Format(time.RFC3339),
			LastActiveAt: sess.LastActiveAt.UTC().Format(time.RFC3339),
			ExpiresAt:    sess.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": views})
}

// --- POST /admin/sessions/{sid}/revoke -------------------------------------

func (s *Server) handleAdminSessionByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	principal, ok := adminPrincipalFrom(r.Context())
	if !ok || principal.UserID == "" {
		writeAdminError(w, reqID, ErrAdminForbidden)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/admin/sessions/")
	sid, ok := strings.CutSuffix(rest, "/revoke")
	if !ok || sid == "" || strings.Contains(sid, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	if err := s.adminAuth.revokeSession(r.Context(), principal.UserID, sid); err != nil {
		writeAdminError(w, reqID, err)
		return
	}
	if sid == principal.SessionID {
		s.clearAdminRefreshCookie(w)
	}
	s.auditAdminAuth(r, "admin_session_revoked", http.StatusOK, principal.Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": sid})
}

// --- /admin/admin-users ----------------------------------------------------
//
// Account administration is reachable only by a super administrator: no other
// role is granted these paths in adminPrincipal.can(), so every other caller is
// stopped with a 403 before the handler runs.

// adminUserView is the safe projection of an account in the administration
// list. The password hash is deliberately absent from the struct, so it cannot
// be serialised by accident.
type adminUserView struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
	Status      int      `json:"status"`
	// ScopeTenantID is null for an account with global reach. Exposing it in
	// the list is what lets an operator answer "who can touch this tenant?"
	// without reading the database.
	ScopeTenantID *string `json:"scope_tenant_id"`
	LastLoginAt   string  `json:"last_login_at"`
	CreatedAt     string  `json:"created_at"`
}

func adminUserViewFrom(u model.AdminUser) adminUserView {
	last := ""
	if u.LastLoginAt != nil {
		last = u.LastLoginAt.UTC().Format(time.RFC3339)
	}
	return adminUserView{
		ID:            u.ID,
		Username:      u.Username,
		DisplayName:   u.DisplayName,
		Roles:         rolesOf(u),
		Status:        u.Status,
		ScopeTenantID: u.ScopeTenantID,
		LastLoginAt:   last,
		CreatedAt:     u.CreatedAt.UTC().Format(time.RFC3339),
	}
}

type adminUserCreateRequest struct {
	Username      string   `json:"username"`
	DisplayName   string   `json:"display_name"`
	Password      string   `json:"password"`
	Roles         []string `json:"roles"`
	ScopeTenantID *string  `json:"scope_tenant_id"`
}

// adminUserScopeRequest sets the tenant scope of an account. It lives on its own
// sub-route rather than in the PATCH body because a pointer field cannot tell
// "clear the scope" apart from "leave it alone" -- both decode to nil, and the
// two mean very different things here.
type adminUserScopeRequest struct {
	ScopeTenantID *string `json:"scope_tenant_id"`
}

// adminUserUpdateRequest is a partial update. A non-empty Roles replaces the
// role set; Status is applied only when present, so "enable" and "disable" stay
// distinguishable from "leave it alone".
type adminUserUpdateRequest struct {
	Roles  []string `json:"roles"`
	Status *int     `json:"status"`
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return
	}
	principal, ok := adminPrincipalFrom(r.Context())
	if !ok {
		writeAdminError(w, reqID, ErrAdminForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		users, err := s.adminAuth.listAdminUsers(r.Context())
		if err != nil {
			writeAdminError(w, reqID, err)
			return
		}
		views := make([]adminUserView, 0, len(users))
		for _, u := range users {
			views = append(views, adminUserViewFrom(u))
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": views})

	case http.MethodPost:
		var in adminUserCreateRequest
		if err := decodeAdminAuthBody(r, &in); err != nil {
			writeAdminError(w, reqID, ErrAdminBadRequest)
			return
		}
		// The scope is a privilege grant, so it is checked before the account
		// is created: an account that should not hold this scope must not come
		// into existence and be fixed afterwards.
		if msg, err := s.authorizeScopeGrant(r.Context(), principal, in.ScopeTenantID); err != nil {
			writeAdminError(w, reqID, ErrAdminInternal)
			return
		} else if msg != "" {
			s.auditAdminAuthResource(r, "admin_user_create_failed",
				"admin_user:"+strings.TrimSpace(in.Username), http.StatusForbidden, principal.Username)
			httpx.Error(w, http.StatusForbidden, "scope_out_of_bounds", msg, reqID)
			return
		}
		user, err := s.adminAuth.createAdminUser(r.Context(), in.Username, in.DisplayName, in.Password, in.Roles, in.ScopeTenantID)
		if err != nil {
			s.auditAdminAuthResource(r, "admin_user_create_failed",
				"admin_user:"+strings.TrimSpace(in.Username), statusForAdminError(err), principal.Username)
			writeAdminError(w, reqID, err)
			return
		}
		s.auditAdminAuthResource(r, "admin_user_created",
			"admin_user:"+user.Username, http.StatusCreated, principal.Username)
		writeJSON(w, http.StatusCreated, adminUserViewFrom(user))

	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) handleAdminUserByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return
	}
	principal, ok := adminPrincipalFrom(r.Context())
	if !ok {
		writeAdminError(w, reqID, ErrAdminForbidden)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/admin/admin-users/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	// The scope lives on its own sub-route: a pointer field cannot distinguish
	// "clear it" from "leave it alone", and here the difference is between
	// confining an account and granting it global reach.
	if id, ok := strings.CutSuffix(rest, "/scope"); ok {
		s.handleAdminUserScope(w, r, id)
		return
	}
	id := rest
	if strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPatch {
		w.Header().Set("Allow", "PATCH")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	var in adminUserUpdateRequest
	if err := decodeAdminAuthBody(r, &in); err != nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}
	if len(in.Roles) == 0 && in.Status == nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}

	var (
		user   model.AdminUser
		err    error
		action string
	)
	if len(in.Roles) > 0 {
		action = "admin_user_roles_changed"
		user, err = s.adminAuth.updateAdminUserRoles(r.Context(), principal.UserID, id, in.Roles)
	} else {
		action = "admin_user_enabled"
		if *in.Status == model.AdminStatusDisabled {
			action = "admin_user_disabled"
		}
		user, err = s.adminAuth.setAdminUserStatus(r.Context(), principal.UserID, id, *in.Status)
	}
	if err != nil {
		s.auditAdminAuthResource(r, action+"_failed", "admin_user:"+id, statusForAdminError(err), principal.Username)
		writeAdminError(w, reqID, err)
		return
	}
	s.auditAdminAuthResource(r, action, "admin_user:"+user.Username, http.StatusOK, principal.Username)
	writeJSON(w, http.StatusOK, adminUserViewFrom(user))
}

// handleAdminUserScope sets or clears the tenant scope of an account.
//
// This is the endpoint that makes delegation possible: create a teacher's
// account, point its scope at the teacher's tenant, and the account can then
// allocate budget to that teacher's students and to nothing else.
func (s *Server) handleAdminUserScope(w http.ResponseWriter, r *http.Request, id string) {
	reqID := r.Header.Get("X-Request-Id")
	if s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return
	}
	principal, ok := adminPrincipalFrom(r.Context())
	if !ok {
		writeAdminError(w, reqID, ErrAdminForbidden)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	var in adminUserScopeRequest
	if err := decodeAdminAuthBody(r, &in); err != nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}
	if msg, err := s.authorizeScopeGrant(r.Context(), principal, in.ScopeTenantID); err != nil {
		writeAdminError(w, reqID, ErrAdminInternal)
		return
	} else if msg != "" {
		s.auditAdminAuthResource(r, "admin_user_scope_change_failed", "admin_user:"+id, http.StatusForbidden, principal.Username)
		httpx.Error(w, http.StatusForbidden, "scope_out_of_bounds", msg, reqID)
		return
	}

	user, err := s.adminAuth.setAdminUserScope(r.Context(), principal.UserID, id, in.ScopeTenantID)
	if err != nil {
		s.auditAdminAuthResource(r, "admin_user_scope_change_failed", "admin_user:"+id, statusForAdminError(err), principal.Username)
		writeAdminError(w, reqID, err)
		return
	}
	s.auditAdminAuthResource(r, "admin_user_scope_changed", "admin_user:"+user.Username, http.StatusOK, principal.Username)
	writeJSON(w, http.StatusOK, adminUserViewFrom(user))
}

// authorizeScopeGrant checks whether the actor may hand out a given tenant
// scope. It returns an operator-facing message rather than a sentinel because
// every failure here is a 403 with a different explanation.
//
// An unscoped actor (super_admin, or any account without a scope) may grant any
// scope, including none. A scoped actor may only grant a scope inside their own
// subtree, and may never grant "no scope": that would hand out precisely the
// global reach they do not themselves have.
func (s *Server) authorizeScopeGrant(ctx context.Context, actor adminPrincipal, scopeTenantID *string) (string, error) {
	if actor.Roles["super_admin"] || actor.ScopeTenantID == nil {
		return "", nil
	}
	target := model.NormalizeOptionalID(scopeTenantID)
	if target == nil {
		return "Only an unscoped administrator can grant an account without a scope", nil
	}
	ok, err := s.pg.TenantIsDescendant(ctx, *actor.ScopeTenantID, *target, true)
	if err != nil {
		return "", err
	}
	if !ok {
		return "The scope tenant is outside your own subtree", nil
	}
	return "", nil
}

// --- shared helpers --------------------------------------------------------

func loginResponseFrom(t adminTokens) adminLoginResponse {
	roles := make([]string, 0, len(t.User.Roles))
	roles = append(roles, t.User.Roles...)
	return adminLoginResponse{
		AccessToken: t.AccessToken,
		ExpiresAt:   t.AccessExpiresAt.UTC().Format(time.RFC3339),
		User: adminAccountView{
			ID:          t.User.ID,
			Username:    t.User.Username,
			DisplayName: t.User.DisplayName,
			Roles:       roles,
		},
	}
}

func decodeAdminAuthBody(r *http.Request, dst any) error {
	defer func() { _, _ = io.Copy(io.Discard, r.Body); _ = r.Body.Close() }()
	dec := json.NewDecoder(io.LimitReader(r.Body, adminAuthBodyLimit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func statusForAdminError(err error) int {
	status, _ := adminErrorStatus(err)
	return status
}

// issueRefreshCookie writes the rotated refresh token as an HttpOnly cookie.
//
// SameSite=Strict plus a Path scoped to /admin/auth means the cookie is not
// attached to ordinary admin API calls, and a cross-site request can never
// carry it.
func (s *Server) issueRefreshCookie(w http.ResponseWriter, tokens adminTokens) {
	maxAge := int(time.Until(tokens.RefreshExpiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminRefreshCookieName,
		Value:    tokens.RefreshToken,
		Path:     adminRefreshCookiePath,
		Expires:  tokens.RefreshExpiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.cfg.AdminCookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearAdminRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminRefreshCookieName,
		Value:    "",
		Path:     adminRefreshCookiePath,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   s.cfg.AdminCookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func readAdminRefreshCookie(r *http.Request) (string, error) {
	c, err := r.Cookie(adminRefreshCookieName)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(c.Value) == "" {
		return "", errors.New("empty refresh cookie")
	}
	return c.Value, nil
}

// checkSameOrigin implements the CSRF defence for cookie-authenticated
// requests: the Origin (or Referer) host must match the host the request was
// addressed to.
func (s *Server) checkSameOrigin(r *http.Request) error {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		referer := strings.TrimSpace(r.Header.Get("Referer"))
		if referer == "" {
			// No browser context information. SameSite=Strict already prevents
			// a cross-site request from carrying the cookie, so a non-browser
			// client (curl, a health check) is allowed through.
			return nil
		}
		parsed, err := url.Parse(referer)
		if err != nil {
			return ErrAdminCSRFRejected
		}
		origin = parsed.Scheme + "://" + parsed.Host
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return ErrAdminCSRFRejected
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return nil
	}
	return ErrAdminCSRFRejected
}

// auditAdminAuth records an authentication event. It is best effort: an audit
// write failure must never change the response the caller receives.
func (s *Server) auditAdminAuth(r *http.Request, action string, status int, actor string) {
	s.auditAdminAuthResource(r, action, r.URL.Path, status, actor)
}

// auditAdminAuthResource is auditAdminAuth with an explicit resource. Account
// administration uses it to record *which* account was touched, which the
// request path alone does not say.
func (s *Server) auditAdminAuthResource(r *http.Request, action, resource string, status int, actor string) {
	if s.pg == nil {
		return
	}
	if actor == "" {
		actor = "anonymous"
	}
	ctx := r.Context()
	_ = s.pg.InsertAdminAudit(ctx, model.AdminAuditLog{
		RequestID:  r.Header.Get("X-Request-Id"),
		Actor:      actor,
		Action:     action,
		Resource:   resource,
		StatusCode: status,
		ClientIP:   s.clientIPFrom(r),
	})
}

// rolesOf is a small helper used by tests and the whoami handler.
func rolesOf(user model.AdminUser) []string {
	set := filterAdminRoles(user.Roles)
	out := make([]string, 0, len(set))
	for role := range set {
		out = append(out, role)
	}
	return out
}
