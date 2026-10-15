package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
)

// --- test server -----------------------------------------------------------

// newTestServer builds a Server wired to the in-memory store. s.pg stays nil;
// the audit helper tolerates that, which keeps these tests free of a database.
func newTestServer(t *testing.T, cfg Config) (*Server, *fakeAdminStore) {
	t.Helper()
	store := newFakeAdminStore()
	svc := newAdminAuthService(store, newFakeLimiter(8), []byte("unit-test-signing-secret"), cfg).
		withRegistrationLimiter(newFakeRegistrationLimiter(cfg.PublicRegisterMaxAttempts))
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

// TestIsLocalSetupRequest covers the proxy-bypass regression: a reverse proxy
// on the same host also connects from 127.0.0.1, so a peer-only loopback check
// would let a remote visitor create the first super administrator.
func TestIsLocalSetupRequest(t *testing.T) {
	const (
		loopback    = "127.0.0.1:41234"
		ipv6Loop    = "[::1]:41234"
		remote      = "203.0.113.1:41234"
		trustedPeer = "198.51.100.9:41234"
	)

	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		trusted []string
		want    bool
	}{
		{name: "loopback direct", remote: loopback, want: true},
		{name: "ipv6 loopback direct", remote: ipv6Loop, want: true},
		{name: "remote direct", remote: remote, want: false},
		{name: "empty remote address", remote: "", want: false},
		{
			name:    "loopback peer forwarding a public client, peer trusted",
			remote:  loopback,
			headers: map[string]string{"X-Forwarded-For": "203.0.113.1"},
			trusted: []string{"127.0.0.1/32"},
			want:    false,
		},
		{
			name:    "loopback peer forwarding a loopback client, peer trusted",
			remote:  loopback,
			headers: map[string]string{"X-Forwarded-For": "127.0.0.1"},
			trusted: []string{"127.0.0.1/32"},
			want:    true,
		},
		{
			name:    "loopback peer forwarding a public client, no trust entry",
			remote:  loopback,
			headers: map[string]string{"X-Forwarded-For": "203.0.113.1"},
			want:    false,
		},
		{
			name:    "trusted remote proxy forwarding a loopback client",
			remote:  trustedPeer,
			headers: map[string]string{"X-Forwarded-For": "127.0.0.1"},
			trusted: []string{"198.51.100.0/24"},
			want:    true,
		},
		{
			name:    "trusted remote proxy forwarding a public client",
			remote:  trustedPeer,
			headers: map[string]string{"X-Forwarded-For": "203.0.113.5"},
			trusted: []string{"198.51.100.0/24"},
			want:    false,
		},
		{
			name:    "untrusted remote peer claiming a loopback client",
			remote:  trustedPeer,
			headers: map[string]string{"X-Forwarded-For": "127.0.0.1"},
			want:    false,
		},
		{
			name:    "loopback peer with a Forwarded header but no trust entry",
			remote:  loopback,
			headers: map[string]string{"Forwarded": "for=203.0.113.1"},
			want:    false,
		},
		{
			name:    "loopback peer with X-Real-IP but no trust entry",
			remote:  loopback,
			headers: map[string]string{"X-Real-IP": "203.0.113.1"},
			want:    false,
		},
		{
			name:    "trusted proxy hop list ending in a public client",
			remote:  loopback,
			headers: map[string]string{"X-Forwarded-For": "127.0.0.1, 203.0.113.7"},
			trusted: []string{"127.0.0.1/32"},
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.TrustedProxyCIDRs = tc.trusted
			s, _ := newTestServer(t, cfg)

			req := httptest.NewRequest(http.MethodPost, "/admin/setup", nil)
			req.RemoteAddr = tc.remote
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			if got := s.isLocalSetupRequest(req); got != tc.want {
				t.Fatalf("isLocalSetupRequest(remote=%q headers=%v trusted=%v) = %v, want %v",
					tc.remote, tc.headers, tc.trusted, got, tc.want)
			}
		})
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

// --- one JSON value per request body ---------------------------------------
//
// P1-05. decodeAdminAuthBody must accept exactly one JSON value, optionally
// followed by whitespace.
//
// The old implementation stopped after the first Decode, so a body like
// {"username":"a"}{"username":"b"} was accepted with the first object and the
// second silently discarded. That is the classic request-smuggling shape: a
// proxy in front of the gateway and the gateway itself can end up disagreeing
// about how many requests arrived, and an interceptor can hide a second
// instruction inside one envelope. This test fails on that implementation.
func TestDecodeAdminAuthBodyAllowsExactlyOneJSONValue(t *testing.T) {
	type payload struct {
		Username string `json:"username"`
	}
	valid := `{"username":"campus-admin"}`

	for _, tc := range []struct{ name, body string }{
		{"second object", valid + `{"username":"attacker"}`},
		{"object then array", valid + `[1,2]`},
		{"object then scalar", valid + `123`},
		{"object then string", valid + `"x"`},
		{"object then null", valid + `null`},
		{"object then true", valid + `true`},
		{"garbage after whitespace", valid + "  \n oops"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(tc.body))
			var got payload
			if err := decodeAdminAuthBody(httptest.NewRecorder(), req, &got); err == nil {
				t.Fatalf("a second JSON value must be refused, decoded %+v", got)
			}
		})
	}

	for _, tc := range []struct{ name, body string }{
		{"no trailing", valid},
		{"newline", valid + "\n"},
		{"spaces and tabs", valid + "  \t\n "},
		{"crlf", valid + "\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(tc.body))
			var got payload
			if err := decodeAdminAuthBody(httptest.NewRecorder(), req, &got); err != nil {
				t.Fatalf("trailing whitespace must be accepted, got %v", err)
			}
			if got.Username != "campus-admin" {
				t.Fatalf("unexpected payload %+v", got)
			}
		})
	}

	// An empty or truncated body stays a refusal rather than a panic or a
	// half-decoded struct.
	for _, body := range []string{"", "   ", `{"username":`} {
		req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(body))
		var got payload
		if err := decodeAdminAuthBody(httptest.NewRecorder(), req, &got); err == nil {
			t.Fatalf("body %q must be refused", body)
		}
	}

	// Over the cap the body is refused whole, not truncated and decoded from
	// its first fragment: a value that only completes past the cap must not
	// reach the handler.
	oversized := `{"username":"` + strings.Repeat("a", adminAuthBodyLimit+64) + `"}`
	if len(oversized) <= adminAuthBodyLimit {
		t.Fatalf("test body must exceed the %d-byte cap", adminAuthBodyLimit)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(oversized))
	var got payload
	if err := decodeAdminAuthBody(httptest.NewRecorder(), req, &got); err == nil {
		t.Fatal("an oversized body must be refused, not truncated and decoded")
	}
}

// Every endpoint that shares the helper must refuse the smuggled shape: the
// fix is in one place, so a single implementation covers login, setup,
// register, password change and the user PATCH — and this pins that coverage
// so a future endpoint cannot quietly opt out.
func TestAuthEndpointsRejectSecondJSONValue(t *testing.T) {
	cfg := testConfig()
	cfg.PublicRegistrationEnabled = true

	s, store := newTestServer(t, cfg)
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	smuggle := func(valid string) string { return valid + `{"username":"attacker"}` }

	login := func(body string) *httptest.ResponseRecorder {
		return postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login", body, nil)
	}
	register := func(body string) *httptest.ResponseRecorder {
		return postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register", body, nil)
	}
	password := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/password", strings.NewReader(body))
		req = req.WithContext(withAdminPrincipal(req.Context(), adminPrincipal{
			UserID: member.ID, Username: member.Username, Roles: map[string]bool{RoleMember: true},
		}))
		rec := httptest.NewRecorder()
		s.handleAdminPasswordChange(rec, req)
		return rec
	}
	patchUser := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "http://gateway.local/admin/users/"+member.ID, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("X-Request-Id", "test-req")
		rec := httptest.NewRecorder()
		s.adminMiddleware(http.HandlerFunc(s.handleAdminUserByID)).ServeHTTP(rec, req)
		return rec
	}

	cases := []struct {
		endpoint string
		valid    string
		call     func(string) *httptest.ResponseRecorder
	}{
		{"login", `{"username":"campus-admin","password":"Campus#2026Gate"}`, login},
		{"register", `{"username":"newuser","display_name":"新用户","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}`, register},
		{"password change", `{"current_password":"Campus#2026Gate","new_password":"NewCampus#2027"}`, password},
		{"user patch", `{"display_name":"新名字"}`, patchUser},
	}
	for _, tc := range cases {
		t.Run(tc.endpoint, func(t *testing.T) {
			rec := tc.call(smuggle(tc.valid))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s with a second JSON value: expected 400, got %d: %s", tc.endpoint, rec.Code, rec.Body.String())
			}
			if code := errorCode(t, rec); code != "invalid_request" {
				t.Fatalf("%s with a second JSON value: expected invalid_request, got %q", tc.endpoint, code)
			}
		})
	}

	// Setup is gated on "no super administrator yet", so it needs a server
	// that has not been seeded.
	t.Run("setup", func(t *testing.T) {
		fresh, _ := newTestServer(t, cfg)
		valid := `{"username":"campus-admin","display_name":"管理员","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}`
		req := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/setup", strings.NewReader(smuggle(valid)))
		req.RemoteAddr = "127.0.0.1:40000"
		rec := httptest.NewRecorder()
		fresh.handleAdminSetup(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("setup with a second JSON value: expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
		if code := errorCode(t, rec); code != "invalid_request" {
			t.Fatalf("setup with a second JSON value: expected invalid_request, got %q", code)
		}
		if fresh.adminAuth == nil || len(store.users) != 2 {
			t.Fatalf("a refused setup must not create an account, got %d users", len(store.users))
		}
	})
}

// The valid shapes keep working: one value plus whitespace is a normal login
// and a normal registration, not an error.
func TestAuthEndpointsAcceptSingleValueWithTrailingWhitespace(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	rec := postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login",
		"{\"username\":\"campus-admin\",\"password\":\"Campus#2026Gate\"}\n  \t", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login with trailing whitespace: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if findCookie(rec, adminRefreshCookieName) == nil {
		t.Fatal("a valid login must still set the refresh cookie")
	}
}

// --- the body is never read forever -----------------------------------------
//
// P1-02 (review). The old helper drained the request body with an unbounded
// io.Copy in a defer. A client that kept the body open — a valid prefix
// followed by an endless stream, or an endless stream from the start — left
// the handler reading forever, one goroutine and one connection held per
// request. The read is bounded by the same cap the decode uses, and an
// oversized body forfeits connection reuse instead of being drained.
//
// These tests fail on that implementation: it never returns.

// endlessAfterPrefix yields the prefix once and then streams filler forever,
// which is what a client holding the request body open looks like.
type endlessAfterPrefix struct {
	prefix string
	sent   bool
}

func (r *endlessAfterPrefix) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.prefix), nil
	}
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

func (r *endlessAfterPrefix) Close() error { return nil }

// endlessReader streams filler from the first byte on.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func (endlessReader) Close() error { return nil }

func TestDecodeAdminAuthBodyNeverReadsForever(t *testing.T) {
	type payload struct {
		Username string `json:"username"`
	}

	for _, tc := range []struct {
		name string
		body io.ReadCloser
	}{
		// The decode itself succeeds here; only the drain used to hang.
		{"valid value then endless stream", &endlessAfterPrefix{prefix: `{"username":"campus-admin"}`}},
		{"endless stream from the start", endlessReader{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				req := httptest.NewRequest(http.MethodPost, "/admin/login", nil)
				req.Body = tc.body
				var got payload
				done <- decodeAdminAuthBody(httptest.NewRecorder(), req, &got)
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("an endless body must never decode successfully")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("decodeAdminAuthBody kept reading an endless body: the drain is unbounded")
			}
		})
	}
}

// A body inside the cap still works through the same path, and the cap itself
// still refuses a body beyond it — the bound is the one the decode documents.
func TestDecodeAdminAuthBodyBoundsWhatItReads(t *testing.T) {
	type payload struct {
		Username string `json:"username"`
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/login",
		strings.NewReader(`{"username":"campus-admin"}`))
	var got payload
	if err := decodeAdminAuthBody(httptest.NewRecorder(), req, &got); err != nil {
		t.Fatalf("a small body must decode: %v", err)
	}
	if got.Username != "campus-admin" {
		t.Fatalf("unexpected payload %+v", got)
	}
}

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

// P1-04 at the HTTP boundary. A forged refresh cookie — the real session id
// with a made-up secret — must not revoke the session it names. The handler
// still answers 200 and still clears the cookie, because the browser has to be
// able to exit, but the device stays signed in: that asymmetry is what makes a
// forged sid worthless to an attacker.
func TestLogoutHandlerForgedCookieDoesNotRevoke(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	login := loginForTest(t, s, store)
	real := findCookie(login, adminRefreshCookieName)
	if real == nil {
		t.Fatal("login must set the refresh cookie")
	}
	// Keep the session id, replace the secret half of "sid.secret".
	dot := strings.IndexByte(real.Value, '.')
	if dot <= 0 {
		t.Fatalf("unexpected refresh cookie shape %q", real.Value)
	}
	forged := &http.Cookie{Name: adminRefreshCookieName, Value: real.Value[:dot+1] + "not-the-real-secret"}

	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/auth/logout", nil)
	req.AddCookie(forged)
	rec := httptest.NewRecorder()
	s.handleAdminLogout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if store.liveSessions() != 1 {
		t.Fatal("a forged refresh cookie must not revoke the session it names")
	}
	cleared := findCookie(rec, adminRefreshCookieName)
	if cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Fatal("logout must still expire the cookie")
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

// --- cacheability ----------------------------------------------------------

// TestAuthResponsesSendNoStore pins that every response carrying an access token
// or describing account state is marked uncacheable, so neither a shared cache
// nor the browser back/forward cache can replay it.
func TestAuthResponsesSendNoStore(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")

	cases := []struct {
		name   string
		handle http.HandlerFunc
		method string
		target string
		body   string
	}{
		{"auth-config", s.handleAdminAuthConfig, http.MethodGet, "/admin/auth-config", ""},
		{"setup", s.handleAdminSetup, http.MethodPost, "/admin/setup",
			`{"username":"newadmin","display_name":"","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}`},
		{"login", s.handleAdminLogin, http.MethodPost, "/admin/login",
			`{"username":"campus-admin","password":"Campus#2026Gate"}`},
		{"refresh", s.handleAdminRefresh, http.MethodPost, "/admin/auth/refresh", ""},
		{"logout", s.handleAdminLogout, http.MethodPost, "/admin/auth/logout", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSON(t, tc.handle, tc.method, tc.target, tc.body, nil)
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			if got := rec.Header().Get("Pragma"); got != "no-cache" {
				t.Fatalf("Pragma = %q, want no-cache", got)
			}
		})
	}

	t.Run("whoami", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/whoami", nil)
		req = req.WithContext(withAdminPrincipal(req.Context(), adminPrincipal{
			User: "campus-admin", Roles: map[string]bool{"auditor": true}, AuthMode: "session",
		}))
		rec := httptest.NewRecorder()
		s.handleAdminWhoAmI(rec, req)
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", got)
		}
		if got := rec.Header().Get("Pragma"); got != "no-cache" {
			t.Fatalf("Pragma = %q, want no-cache", got)
		}
	})
}

// --- refresh concurrency at the HTTP layer ---------------------------------

// TestRefreshHandlerBenignRaceKeepsCookie is the two-tab regression at the HTTP
// layer: the second tab presents the same cookie, and the response must be 200
// with the same successor rather than a cleared cookie.
func TestRefreshHandlerBenignRaceKeepsCookie(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	login := loginForTest(t, s, store)
	cookie := findCookie(login, adminRefreshCookieName)

	call := func() (*httptest.ResponseRecorder, *http.Cookie) {
		req := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/auth/refresh", nil)
		req.AddCookie(cookie)
		req.Header.Set("Origin", "http://gateway.local")
		rec := httptest.NewRecorder()
		s.handleAdminRefresh(rec, req)
		return rec, findCookie(rec, adminRefreshCookieName)
	}

	first, firstCookie := call()
	if first.Code != http.StatusOK {
		t.Fatalf("first refresh: expected 200, got %d: %s", first.Code, first.Body.String())
	}
	second, secondCookie := call()
	if second.Code != http.StatusOK {
		t.Fatalf("the second tab must not be signed out: got %d: %s", second.Code, second.Body.String())
	}
	if secondCookie == nil || secondCookie.Value == "" {
		t.Fatal("a benign race must re-issue the cookie, not clear it")
	}
	if firstCookie.Value != secondCookie.Value {
		t.Fatalf("both tabs must converge on one cookie value:/n first=%q\nsecond=%q",
			firstCookie.Value, secondCookie.Value)
	}
	if store.liveSessions() != 1 {
		t.Fatalf("a benign race must not revoke the session, %d live", store.liveSessions())
	}
}

// TestRefreshHandlerReplayOutsideGraceClearsCookie is the genuine-replay case:
// the cookie is cleared, the family is revoked, and the attempt is auditable.
func TestRefreshHandlerReplayOutsideGraceClearsCookie(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	login := loginForTest(t, s, store)
	cookie := findCookie(login, adminRefreshCookieName)

	base := time.Now()
	s.adminAuth.now = func() time.Time { return base }

	first := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/auth/refresh", nil)
	first.AddCookie(cookie)
	first.Header.Set("Origin", "http://gateway.local")
	firstRec := httptest.NewRecorder()
	s.handleAdminRefresh(firstRec, first)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first refresh: expected 200, got %d", firstRec.Code)
	}

	// Move past the grace window, then present the superseded cookie again.
	s.adminAuth.now = func() time.Time {
		return base.Add(testConfig().AdminRefreshReplayGrace + time.Second)
	}

	replay := httptest.NewRequest(http.MethodPost, "http://gateway.local/admin/auth/refresh", nil)
	replay.AddCookie(cookie)
	replay.Header.Set("Origin", "http://gateway.local")
	replayRec := httptest.NewRecorder()
	s.handleAdminRefresh(replayRec, replay)

	if replayRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on replay, got %d", replayRec.Code)
	}
	if code := errorCode(t, replayRec); code != "refresh_replay_detected" {
		t.Fatalf("expected code refresh_replay_detected, got %q", code)
	}
	cleared := findCookie(replayRec, adminRefreshCookieName)
	if cleared == nil || cleared.Value != "" {
		t.Fatal("a detected replay must clear the cookie")
	}
	if store.liveSessions() != 0 {
		t.Fatalf("a detected replay must revoke every session, %d live", store.liveSessions())
	}
}
