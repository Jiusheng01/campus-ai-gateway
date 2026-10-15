package app

import (
	"bytes"
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
	// RegistrationEnabled is false while the system still needs initialising,
	// even when public registration is configured on: the first super
	// administrator must come from the local setup flow, so offering a
	// registration form would lead straight into a 409.
	RegistrationEnabled bool `json:"registration_enabled"`
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
		out.RegistrationEnabled = s.cfg.PublicRegistrationEnabled && !needsSetup
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

// logoutAuditAction names the audit row a logout request writes.
//
// The HTTP response is a 200 either way — the browser always gets its cookie
// cleared, because it has to be able to leave — but the trail must be able to
// tell a device that signed out from a request that presented a token the
// session did not own. The latter revokes nothing, and recording it as a
// successful logout would hide exactly the signal an operator investigating
// session tampering looks for.
func logoutAuditAction(revoked bool) string {
	if revoked {
		return "admin_logout"
	}
	return "admin_logout_rejected"
}

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
		revoked, err := s.adminAuth.logout(r.Context(), raw)
		if err != nil {
			log.Printf("admin logout: %v", err)
		}
		s.auditAdminAuth(r, logoutAuditAction(revoked), http.StatusOK, "")
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

// --- shared helpers --------------------------------------------------------

func loginResponseFrom(t adminTokens) adminLoginResponse {
	return adminLoginResponse{
		AccessToken: t.AccessToken,
		ExpiresAt:   t.AccessExpiresAt.UTC().Format(time.RFC3339),
		User: adminAccountView{
			ID:          t.User.ID,
			Username:    t.User.Username,
			DisplayName: t.User.DisplayName,
			// Normalised so the client never has to reason about whether
			// "member" sits alongside a management role.
			Roles: NormalizeAccountRoles(t.User.Roles),
		},
	}
}

// decodeAdminAuthBody decodes exactly one JSON value from the request body.
//
// Three boundaries are enforced together, because each on its own is
// insufficient:
//
//   - DisallowUnknownFields refuses a body carrying "roles" or "status" where
//     none is expected, so a privilege-injection attempt is a loud 400 instead
//     of a silently ignored field.
//   - The body is read at most adminAuthBodyLimit+1 bytes, so an oversized
//     body is refused whole rather than truncated and decoded from its first
//     fragment.
//   - The second Decode accepts nothing but io.EOF. A valid object followed by
//     a second JSON value is the canonical request-smuggling shape — an
//     interceptor can hide a second instruction in one envelope, and a proxy
//     in front of the gateway can end up disagreeing with the gateway about
//     how many requests arrived. Only whitespace may trail the value.
//
// The body is never drained. A body within the cap was consumed in full by the
// bounded read, so there is nothing left; a body beyond the cap is being
// refused, and the server closes the connection when the handler returns with
// the request unread — the correct outcome for an oversized or double-value
// body. An unbounded drain used to let a client stream an endless request and
// hold the handler open indefinitely, one goroutine and one connection per
// request; reusing a connection for a request we reject is worth less than
// never being held open.
//
// The helper returns the decoding error itself; every caller maps any failure
// to ErrAdminBadRequest, so the distinct shapes collapse to one stable
// 400 invalid_request.
func decodeAdminAuthBody(r *http.Request, dst any) error {
	// One byte past the cap is enough to tell "exactly at the cap" from "past
	// the cap", so an oversized body is refused outright instead of being
	// truncated and quietly decoded from its first fragment.
	raw, err := io.ReadAll(io.LimitReader(r.Body, adminAuthBodyLimit+1))
	_ = r.Body.Close()
	if err != nil {
		return err
	}
	if len(raw) > adminAuthBodyLimit {
		return errAdminAuthBodyTooLarge
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return errTrailingJSONValue
	}
	return nil
}

// errAdminAuthBodyTooLarge is returned when the body exceeds the cap. The cap
// is small — a few short strings — so anything larger is an attack or a bug.
var (
	errAdminAuthBodyTooLarge = errors.New("request body too large")
	errTrailingJSONValue     = errors.New("request body must contain exactly one JSON value")
)

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
		Resource:   r.URL.Path,
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
