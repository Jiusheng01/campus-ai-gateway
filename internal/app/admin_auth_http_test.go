package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
)

// --- test server -----------------------------------------------------------

// newTestServer builds a Server wired to the in-memory store. s.pg stays nil;
// the audit helper tolerates that, which keeps these tests free of a database.
func newTestServer(t *testing.T, cfg Config) (*Server, *fakeAdminStore) {
	t.Helper()
	store := newFakeAdminStore()
	svc := newAdminAuthService(store, newFakeLimiter(8), []byte("unit-test-signing-secret"), cfg)
	return &Server{cfg: cfg, adminAuth: svc, mux: http.NewServeMux()}, store
}

func postJSON(t *testing.T, h http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "test-req")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	return out
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	body := decodeBody(t, rec)
	e, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no error object: %s", rec.Body.String())
	}
	code, _ := e["code"].(string)
	return code
}

func findCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// --- client address resolution --------------------------------------------

func TestClientIPFromIgnoresForwardedHeaderFromUntrustedPeer(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	req := httptest.NewRequest(http.MethodGet, "/admin/status", nil)
	req.RemoteAddr = "203.0.113.9:51234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := s.clientIPFrom(req); got != "203.0.113.9" {
		t.Fatalf("an untrusted peer must not be able to spoof X-Forwarded-For, got %q", got)
	}
}

func TestClientIPFromUsesForwardedHeaderFromTrustedProxy(t *testing.T) {
	cfg := testConfig()
	cfg.TrustedProxyCIDRs = []string{"10.0.0.0/8"}
	s, _ := newTestServer(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/admin/status", nil)
	req.RemoteAddr = "10.0.0.2:51234"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")

	if got := s.clientIPFrom(req); got != "198.51.100.7" {
		t.Fatalf("expected the forwarded client address, got %q", got)
	}
}

func TestClientIPFromSkipsTrustedProxyChain(t *testing.T) {
	cfg := testConfig()
	cfg.TrustedProxyCIDRs = []string{"10.0.0.0/8", "127.0.0.1/32"}
	s, _ := newTestServer(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/admin/status", nil)
	req.RemoteAddr = "10.0.0.2:51234"
	// client, then an internal proxy hop, then the peer we already know about
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.5, 10.0.0.2")

	if got := s.clientIPFrom(req); got != "198.51.100.7" {
		t.Fatalf("expected the first non-proxy hop, got %q", got)
	}
}

func TestClientIPFromAcceptsBareAddressInTrustedList(t *testing.T) {
	cfg := testConfig()
	cfg.TrustedProxyCIDRs = []string{"127.0.0.1"}
	s, _ := newTestServer(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/admin/status", nil)
	req.RemoteAddr = "127.0.0.1:51234"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")

	if got := s.clientIPFrom(req); got != "198.51.100.7" {
		t.Fatalf("a bare address must be accepted as a single-host trust entry, got %q", got)
	}
}

func TestIsLoopbackRequest(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:1234": true,
		"[::1]:1234":     true,
		"192.168.1.20:80": false,
		"10.0.0.1:80":    false,
		"":               false,
	}
	for addr, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = addr
		if got := isLoopbackRequest(req); got != want {
			t.Fatalf("isLoopbackRequest(%q) = %v, want %v", addr, got, want)
		}
	}
}

// --- CSRF ------------------------------------------------------------------

func TestCheckSameOrigin(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	cases := []struct {
		name    string
		host    string
		origin  string
		referer string
		wantErr bool
	}{
		{"no headers is allowed for non-browser clients", "gateway.local", "", "", false},
		{"matching origin", "gateway.local", "http://gateway.local", "", false},
		{"matching origin with port", "gateway.local:8080", "http://gateway.local:8080", "", false},
		{"case-insensitive host", "gateway.local", "http://GATEWAY.LOCAL", "", false},
		{"port mismatch", "gateway.local", "http://gateway.local:8080", "", true},
		{"foreign origin", "gateway.local", "http://evil.example", "", true},
		{"matching referer", "gateway.local", "", "http://gateway.local/admin-ui/", false},
		{"foreign referer", "gateway.local", "", "http://evil.example/page", true},
		{"garbage origin", "gateway.local", "::::", "", true},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/admin/auth/refresh", nil)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		if tc.referer != "" {
			req.Header.Set("Referer", tc.referer)
		}
		err := s.checkSameOrigin(req)
		if tc.wantErr && err == nil {
			t.Fatalf("%s: expected rejection", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("%s: unexpected rejection: %v", tc.name, err)
		}
	}
}

// --- GET /admin/auth-config ------------------------------------------------

func TestAuthConfigExposesNoTokenConcept(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	rec := postJSON(t, http.HandlerFunc(s.handleAdminAuthConfig), http.MethodGet, "/admin/auth-config", "", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	raw := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"token", "admin_token", "secret", "password_hash", "setup_token", "initialization"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("the auth-config response must not mention %q: %s", forbidden, rec.Body.String())
		}
	}
	body := decodeBody(t, rec)
	if body["needs_setup"] != true {
		t.Fatalf("expected needs_setup=true on a fresh install, got %v", body["needs_setup"])
	}
	if body["login_enabled"] != true {
		t.Fatal("login must be enabled in session mode")
	}
	if body["auth_mode"] != "session" {
		t.Fatalf("unexpected auth_mode: %v", body["auth_mode"])
	}
}

func TestAuthConfigReportsNeedsSetupFalseAfterSetup(t *testing.T) {
	cfg := testConfig()
	s, store := newTestServer(t, cfg)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	rec := postJSON(t, http.HandlerFunc(s.handleAdminAuthConfig), http.MethodGet, "/admin/auth-config", "", nil)
	if body := decodeBody(t, rec); body["needs_setup"] != false {
		t.Fatalf("expected needs_setup=false once an account exists, got %v", body["needs_setup"])
	}
}

func TestAuthConfigRejectsNonGet(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	rec := postJSON(t, http.HandlerFunc(s.handleAdminAuthConfig), http.MethodPost, "/admin/auth-config", "", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// --- POST /admin/login -----------------------------------------------------

func TestLoginHandlerSetsHardenedCookie(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	rec := postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login",
		`{"username":"campus-admin","password":"Campus#2026Gate"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	c := findCookie(rec, adminRefreshCookieName)
	if c == nil {
		t.Fatal("login must set the refresh cookie")
	}
	if !c.HttpOnly {
		t.Fatal("the refresh cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("the refresh cookie must be SameSite=Strict, got %v", c.SameSite)
	}
	if c.Path != adminRefreshCookiePath {
		t.Fatalf("the refresh cookie path must be %q, got %q", adminRefreshCookiePath, c.Path)
	}
	if c.MaxAge <= 0 {
		t.Fatal("the refresh cookie must have a positive Max-Age")
	}

	body := decodeBody(t, rec)
	if body["access_token"] == nil || body["access_token"] == "" {
		t.Fatal("login must return an access token")
	}
	if strings.Contains(rec.Body.String(), "password_hash") {
		t.Fatal("the response must never contain the password hash")
	}
	user, _ := body["user"].(map[string]any)
	if user["username"] != "campus-admin" {
		t.Fatalf("unexpected user payload: %v", body["user"])
	}
}

func TestLoginHandlerRejectsBadCredentials(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	rec := postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login",
		`{"username":"campus-admin","password":"nope"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "invalid_credentials" {
		t.Fatalf("expected code invalid_credentials, got %q", code)
	}
	if strings.Contains(rec.Body.String(), "password") && strings.Contains(rec.Body.String(), "incorrect") {
		// The message may say "username or password is incorrect"; what matters
		// is that it does not distinguish the two cases.
		if strings.Contains(strings.ToLower(rec.Body.String()), "username not found") {
			t.Fatal("the error must not reveal whether the username exists")
		}
	}
	if c := findCookie(rec, adminRefreshCookieName); c != nil && c.Value != "" {
		t.Fatal("a failed login must not set a refresh cookie")
	}
}

func TestLoginHandlerRejectsMalformedBody(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	rec := postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login", `{`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestLoginHandlerRateLimited(t *testing.T) {
	cfg := testConfig()
	cfg.AdminLoginMaxAttempts = 3
	store := newFakeAdminStore()
	svc := newAdminAuthService(store, newFakeLimiter(3), []byte("unit-test-signing-secret"), cfg)
	s := &Server{cfg: cfg, adminAuth: svc}
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	for i := 0; i < 3; i++ {
		postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login",
			`{"username":"campus-admin","password":"nope"}`, nil)
	}
	rec := postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login",
		`{"username":"campus-admin","password":"Campus#2026Gate"}`, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "too_many_attempts" {
		t.Fatalf("expected code too_many_attempts, got %q", code)
	}
}

// --- POST /admin/setup -----------------------------------------------------

func TestSetupHandlerRejectsRemoteCaller(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	req := httptest.NewRequest(http.MethodPost, "/admin/setup",
		strings.NewReader(`{"username":"a","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}`))
	req.RemoteAddr = "192.168.1.50:40000"
	rec := httptest.NewRecorder()
	s.handleAdminSetup(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "setup_required_but_remote_forbidden" {
		t.Fatalf("expected code setup_required_but_remote_forbidden, got %q", code)
	}
}

func TestSetupHandlerAllowsLocalhostAndSignsIn(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	req := httptest.NewRequest(http.MethodPost, "/admin/setup",
		strings.NewReader(`{"username":"campus-admin","display_name":"管理员","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}`))
	req.RemoteAddr = "127.0.0.1:40000"
	rec := httptest.NewRecorder()
	s.handleAdminSetup(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if c := findCookie(rec, adminRefreshCookieName); c == nil || c.Value == "" {
		t.Fatal("setup must sign the operator in")
	}
	if body := decodeBody(t, rec); body["access_token"] == nil {
		t.Fatal("setup must return an access token")
	}
}

func TestSetupHandlerRejectsMismatchedConfirmation(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	req := httptest.NewRequest(http.MethodPost, "/admin/setup",
		strings.NewReader(`{"username":"campus-admin","password":"Campus#2026Gate","confirm_password":"Different#2026"}`))
	req.RemoteAddr = "127.0.0.1:40000"
	rec := httptest.NewRecorder()
	s.handleAdminSetup(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSetupHandlerRejectedWhenAccountExists(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedAdmin(t, store, "existing", "Existing#2026Pass")

	req := httptest.NewRequest(http.MethodPost, "/admin/setup",
		strings.NewReader(`{"username":"second","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}`))
	req.RemoteAddr = "127.0.0.1:40000"
	rec := httptest.NewRecorder()
	s.handleAdminSetup(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "setup_not_allowed" {
		t.Fatalf("expected code setup_not_allowed, got %q", code)
	}
}

func TestSetupHandlerDisabledInTokenMode(t *testing.T) {
	cfg := testConfig()
	cfg.AdminAuthMode = "token"
	s, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodPost, "/admin/setup",
		strings.NewReader(`{"username":"a","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}`))
	req.RemoteAddr = "127.0.0.1:40000"
	rec := httptest.NewRecorder()
	s.handleAdminSetup(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 in token mode, got %d", rec.Code)
	}
}

// --- POST /admin/auth/refresh ---------------------------------------------

func loginForTest(t *testing.T, s *Server, store *fakeAdminStore) *httptest.ResponseRecorder {
	t.Helper()
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	rec := postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login",
		`{"username":"campus-admin","password":"Campus#2026Gate"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	return rec
}

func TestRefreshHandlerRotatesCookie(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	login := loginForTest(t, s, store)
	cookie := findCookie(login, adminRefreshCookieName)

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/auth/refresh", nil)
	req.AddCookie(cookie)
	req.Header.Set("Origin", "http://gateway.local")
	rec := httptest.NewRecorder()
	s.handleAdminRefresh(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rotated := findCookie(rec, adminRefreshCookieName)
	if rotated == nil || rotated.Value == cookie.Value {
		t.Fatal("refresh must issue a new cookie value")
	}
	if body := decodeBody(t, rec); body["access_token"] == nil {
		t.Fatal("refresh must return a new access token")
	}
}

func TestRefreshHandlerWithoutCookie(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/auth/refresh", nil)
	rec := httptest.NewRecorder()
	s.handleAdminRefresh(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "unauthenticated" {
		t.Fatalf("expected code unauthenticated, got %q", code)
	}
}

func TestRefreshHandlerRejectsCrossOrigin(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	login := loginForTest(t, s, store)
	cookie := findCookie(login, adminRefreshCookieName)

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/auth/refresh", nil)
	req.AddCookie(cookie)
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	s.handleAdminRefresh(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "csrf_rejected" {
		t.Fatalf("expected code csrf_rejected, got %q", code)
	}
}

func TestRefreshHandlerClearsCookieOnFailure(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: adminRefreshCookieName, Value: "bogus.secret"})
	rec := httptest.NewRecorder()
	s.handleAdminRefresh(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	cleared := findCookie(rec, adminRefreshCookieName)
	if cleared == nil || cleared.Value != "" {
		t.Fatal("a failed refresh must clear the cookie")
	}
}

// --- POST /admin/logout ----------------------------------------------------

func TestLogoutHandlerRevokesAndClears(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	login := loginForTest(t, s, store)
	cookie := findCookie(login, adminRefreshCookieName)

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/auth/logout", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleAdminLogout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if store.liveSessions() != 0 {
		t.Fatal("logout must revoke the session")
	}
	cleared := findCookie(rec, adminRefreshCookieName)
	if cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Fatal("logout must expire the cookie")
	}
}

func TestLogoutHandlerRejectsCrossOrigin(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/auth/logout", nil)
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	s.handleAdminLogout(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

// --- POST /admin/password --------------------------------------------------

func TestPasswordChangeHandlerClearsCookie(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	login := loginForTest(t, s, store)
	token, _ := decodeBody(t, login)["access_token"].(string)

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/password",
		strings.NewReader(`{"current_password":"Campus#2026Gate","new_password":"NewCampus#2027"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	principal, err := s.adminAuth.authenticate(req.Context(), token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	req = req.WithContext(withAdminPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	s.handleAdminPasswordChange(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.liveSessions() != 0 {
		t.Fatal("a password change must revoke every session")
	}
	if cleared := findCookie(rec, adminRefreshCookieName); cleared == nil || cleared.Value != "" {
		t.Fatal("the refresh cookie must be cleared after a password change")
	}
}

func TestPasswordChangeHandlerRejectsWrongCurrentPassword(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	login := loginForTest(t, s, store)
	token, _ := decodeBody(t, login)["access_token"].(string)
	principal, err := s.adminAuth.authenticate(t.Context(), token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/password",
		strings.NewReader(`{"current_password":"Wrong#2026Pass","new_password":"NewCampus#2027"}`))
	req = req.WithContext(withAdminPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	s.handleAdminPasswordChange(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if store.liveSessions() != 1 {
		t.Fatal("a rejected change must leave the session alone")
	}
}

// --- GET /admin/sessions and revoke ---------------------------------------

func TestSessionsHandlerListsOwnSessions(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	login := loginForTest(t, s, store)
	token, _ := decodeBody(t, login)["access_token"].(string)
	principal, err := s.adminAuth.authenticate(t.Context(), token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/admin/sessions", nil)
	req = req.WithContext(withAdminPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	s.handleAdminSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	items, _ := body["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one session, got %v", body["data"])
	}
	first, _ := items[0].(map[string]any)
	if first["current"] != true {
		t.Fatal("the caller's own session must be flagged as current")
	}
	if strings.Contains(rec.Body.String(), "refresh") {
		t.Fatal("the session list must never expose refresh material")
	}
}

func TestSessionRevokeHandler(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	login := loginForTest(t, s, store)
	token, _ := decodeBody(t, login)["access_token"].(string)
	principal, err := s.adminAuth.authenticate(t.Context(), token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost,
		"http://gateway.local/admin/sessions/"+principal.SessionID+"/revoke", nil)
	req = req.WithContext(withAdminPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	s.handleAdminSessionByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.liveSessions() != 0 {
		t.Fatal("revoking the current session must invalidate it")
	}
	if cleared := findCookie(rec, adminRefreshCookieName); cleared == nil {
		t.Fatal("revoking your own session must clear the cookie")
	}
}

func TestSessionRevokeHandlerRejectsBadPath(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	login := loginForTest(t, s, store)
	token, _ := decodeBody(t, login)["access_token"].(string)
	principal, _ := s.adminAuth.authenticate(t.Context(), token)

	for _, path := range []string{"/admin/sessions//revoke", "/admin/sessions/abc/def/revoke", "/admin/sessions/abc"} {
		req := httptest.NewRequest(http.MethodPost, "http://gateway.local"+path, nil)
		req = req.WithContext(withAdminPrincipal(req.Context(), principal))
		rec := httptest.NewRecorder()
		s.handleAdminSessionByID(rec, req)
		if rec.Code == http.StatusOK {
			t.Fatalf("%s must not succeed", path)
		}
	}
	_ = store
}

// --- adminMiddleware regression -------------------------------------------

// TestAdminMiddlewareDoesNotReport503ForAuthFailures is the regression test for
// the v1 behaviour that matched error text containing "required" and returned
// 503, telling clients to retry an authentication failure forever.
func TestAdminMiddlewareDoesNotReport503ForAuthFailures(t *testing.T) {
	cases := []struct {
		name   string
		cfg    Config
		header string
	}{
		{"session mode, no credential", testConfig(), ""},
		{"session mode, garbage token", testConfig(), "Bearer not-a-token"},
	}
	for _, tc := range cases {
		s, _ := newTestServer(t, tc.cfg)
		handler := s.adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, "http://gateway.local/admin/status", nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d (%s)", tc.name, rec.Code, rec.Body.String())
		}
		if code := errorCode(t, rec); code == "admin_api_disabled" {
			t.Fatalf("%s: an authentication failure must not be reported as admin_api_disabled", tc.name)
		}
	}
}

func TestAdminMiddlewareTokenModeUsesBreakGlassToken(t *testing.T) {
	cfg := testConfig()
	cfg.AdminAuthMode = "token"
	cfg.AdminToken = "super-secret-break-glass"
	s, _ := newTestServer(t, cfg)

	handler := s.adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := adminPrincipalFrom(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"user": p.User, "mode": p.AuthMode})
	}))

	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/admin/whoami", nil)
	req.Header.Set("Authorization", "Bearer super-secret-break-glass")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := decodeBody(t, rec); body["mode"] != "token" {
		t.Fatalf("expected token mode, got %v", body["mode"])
	}
}

func TestAdminMiddlewareTokenModeWithoutTokenIs503(t *testing.T) {
	cfg := testConfig()
	cfg.AdminAuthMode = "token"
	cfg.AdminToken = ""
	s, _ := newTestServer(t, cfg)

	handler := s.adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://gateway.local/admin/status", nil))

	// A missing configuration really is an operational failure, so 503 is
	// correct here. This is the one case where it is.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when the mode cannot serve requests, got %d", rec.Code)
	}
}

// seedAuditor creates a non-super-admin account and returns its access token, so
// the middleware's RBAC decision can be exercised end to end.
func seedAuditor(t *testing.T, s *Server, store *fakeAdminStore) string {
	t.Helper()
	hash, err := security.HashPassword("Auditor#2026Pass")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := store.CreateFirstAdminUser(t.Context(), "auditor", "Auditor", hash, []string{"auditor"}); err != nil {
		t.Fatalf("seed auditor: %v", err)
	}
	rec := postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login",
		`{"username":"auditor","password":"Auditor#2026Pass"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("auditor login failed: %d %s", rec.Code, rec.Body.String())
	}
	token, _ := decodeBody(t, rec)["access_token"].(string)
	return token
}

func TestAdminMiddlewareEnforcesRBAC(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	token := seedAuditor(t, s, store)

	handler := s.adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// An auditor may read logs but must not mutate tenants.
	req := httptest.NewRequest(http.MethodPut, "http://gateway.local/admin/tenants/abc", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "admin_forbidden" {
		t.Fatalf("expected code admin_forbidden, got %q", code)
	}
}

func TestSelfServiceEndpointsBypassRBAC(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	token := seedAuditor(t, s, store)

	handler := s.adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// The account holds only the auditor role, which grants none of these paths
	// through the RBAC table. They must still be reachable, because rotating
	// your own password is not a privileged operation.
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/admin/whoami"},
		{http.MethodPost, "/admin/password"},
		{http.MethodGet, "/admin/sessions"},
		{http.MethodPost, "/admin/sessions/abc/revoke"},
		{http.MethodPost, "/admin/logout"},
	} {
		req := httptest.NewRequest(tc.method, "http://gateway.local"+tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s must be allowed for any signed-in account, got %d (%s)",
				tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}
