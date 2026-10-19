package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

// --- helpers ---------------------------------------------------------------

// newTestServerWithRegistration builds a server whose registration throttle the
// test can inspect and break, which the shared helper does not expose.
func newTestServerWithRegistration(t *testing.T, cfg Config) (*Server, *fakeAdminStore, *fakeRegistrationLimiter) {
	t.Helper()
	store := newFakeAdminStore()
	reg := newFakeRegistrationLimiter(cfg.PublicRegisterMaxAttempts)
	svc := newAdminAuthService(store, newFakeLimiter(8), []byte("unit-test-signing-secret"), cfg).
		withRegistrationLimiter(reg)
	return &Server{cfg: cfg, adminAuth: svc, mux: http.NewServeMux()}, store, reg
}

// seedMember inserts an ordinary account, bypassing registration.
func seedMember(t *testing.T, store *fakeAdminStore, username, password string) model.AdminUser {
	t.Helper()
	hash, err := security.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := store.CreateUser(context.Background(), username, "Test Member", hash, []string{RoleMember})
	if err != nil {
		t.Fatalf("seed member: %v", err)
	}
	return u
}

// seedSetupAdmin creates the first super administrator the way setup does.
func seedSetupAdmin(t *testing.T, s *Server, username, password string) model.AdminUser {
	t.Helper()
	user, err := s.adminAuth.setup(context.Background(), username, "Test Admin", password)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	return user
}

func registerBody(username, displayName, password string) string {
	return `{"username":"` + username + `","display_name":"` + displayName +
		`","password":"` + password + `","confirm_password":"` + password + `"}`
}

// accessTokenFor signs a real session in, so RBAC probes go through the same
// authentication path a browser would.
func accessTokenFor(t *testing.T, s *Server, username, password string) string {
	t.Helper()
	tokens, err := s.adminAuth.login(context.Background(), username, password, "10.0.0.9", "test-agent")
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	return tokens.AccessToken
}

// callThroughAdminMiddleware runs a request through the real authentication and
// RBAC middleware. A 200 means the inner handler ran, so it reports the
// authorisation decision rather than a handler's own behaviour.
func callThroughAdminMiddleware(t *testing.T, s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Request-Id", "test-req")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.adminMiddleware(inner).ServeHTTP(rec, req)
	return rec
}

// --- 1. setup owns the window before the first super administrator ----------

func TestRegisterRefusedBeforeFirstSuperAdminExists(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())

	rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("zhangsan", "张三", "Campus#2026Gate"), nil)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 before setup, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "setup_required" {
		t.Fatalf("expected setup_required, got %q", code)
	}
	if len(store.users) != 0 {
		t.Fatalf("a refused registration must not create an account, got %d", len(store.users))
	}
}

func TestRegisterAllowedOnceASuperAdminExists(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("zhangsan", "张三", "Campus#2026Gate"), nil)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(store.users) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(store.users))
	}
}

// A member must not be able to close the setup window. This is the exact
// sequence that would otherwise leave a deployment with no administrator.
func TestRegisteredMemberDoesNotCloseSetup(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	if rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("zhangsan", "张三", "Campus#2026Gate"), nil); rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}

	// Remove the only super administrator directly, then confirm setup reopens
	// even though an ordinary account still exists.
	for id, u := range store.users {
		if hasSuperAdmin(u.Roles) {
			u.Roles = []string{RoleMember}
			store.users[id] = u
		}
	}
	needsSetup, err := s.adminAuth.needsSetup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !needsSetup {
		t.Fatal("setup must reopen when no super administrator remains, even if members exist")
	}
}

func TestAuthConfigHidesRegistrationUntilSetupIsDone(t *testing.T) {
	s, _, _ := newTestServerWithRegistration(t, testConfig())

	req := httptest.NewRequest(http.MethodGet, "/admin/auth-config", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	s.handleAdminAuthConfig(rec, req)

	body := decodeBody(t, rec)
	if body["needs_setup"] != true {
		t.Fatalf("expected needs_setup true, got %v", body["needs_setup"])
	}
	if body["registration_enabled"] != false {
		t.Fatalf("registration must not be offered before setup, got %v", body["registration_enabled"])
	}

	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	rec = httptest.NewRecorder()
	s.handleAdminAuthConfig(rec, req)
	body = decodeBody(t, rec)
	if body["registration_enabled"] != true {
		t.Fatalf("registration must be offered once setup is done, got %v", body["registration_enabled"])
	}
}

// P1-03. With the switch off, auth-config never advertises registration — not
// before setup and not after it. The login page has no other source of truth
// for the register tab, so this is what keeps a ghost entry off the screen
// after the default flipped to false.
func TestAuthConfigHidesRegistrationWhenDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.PublicRegistrationEnabled = false
	s, _, _ := newTestServerWithRegistration(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/admin/auth-config", nil)
	req.RemoteAddr = "127.0.0.1:1234"

	rec := httptest.NewRecorder()
	s.handleAdminAuthConfig(rec, req)
	if body := decodeBody(t, rec); body["registration_enabled"] != false {
		t.Fatalf("registration must be hidden before setup, got %v", body["registration_enabled"])
	}

	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	rec = httptest.NewRecorder()
	s.handleAdminAuthConfig(rec, req)
	if body := decodeBody(t, rec); body["registration_enabled"] != false {
		t.Fatalf("registration must stay hidden after setup when the switch is off, got %v", body["registration_enabled"])
	}
}

// --- 2. registration creates a member and signs it in -----------------------

func TestRegisterCreatesMemberAndSession(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("zhangsan", "张三", "Campus#2026Gate"), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Auto sign-in: the response carries an access token and a refresh cookie,
	// so the user does not have to retype the password they just chose.
	body := decodeBody(t, rec)
	if token, _ := body["access_token"].(string); token == "" {
		t.Fatal("registration must return an access token")
	}
	if findCookie(rec, adminRefreshCookieName) == nil {
		t.Fatal("registration must set the refresh cookie")
	}

	user, err := store.FindAdminUserByUsername(context.Background(), "zhangsan")
	if err != nil {
		t.Fatalf("registered account not found: %v", err)
	}
	if len(user.Roles) != 1 || user.Roles[0] != RoleMember {
		t.Fatalf("registered account must hold exactly [member], got %v", user.Roles)
	}
	if user.Status != model.AdminStatusEnabled {
		t.Fatalf("registered account must be enabled, got %d", user.Status)
	}
	if user.AuthVersion != 1 {
		t.Fatalf("registered account must start at auth_version 1, got %d", user.AuthVersion)
	}

	var loginMethod string
	for _, sess := range store.sessions {
		if sess.UserID == user.ID {
			loginMethod = sess.LoginMethod
		}
	}
	if loginMethod != loginMethodRegister {
		t.Fatalf("session login_method = %q, want %q", loginMethod, loginMethodRegister)
	}
}

func TestRegisterPasswordIsHashedNotStored(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("zhangsan", "张三", "Campus#2026Gate"), nil)

	user, err := store.FindAdminUserByUsername(context.Background(), "zhangsan")
	if err != nil {
		t.Fatal(err)
	}
	if user.PasswordHash == "Campus#2026Gate" || user.PasswordHash == "" {
		t.Fatal("the plaintext password must never be stored")
	}
	ok, _, err := security.VerifyPassword(user.PasswordHash, "Campus#2026Gate")
	if err != nil || !ok {
		t.Fatalf("stored hash must verify the password: ok=%v err=%v", ok, err)
	}
}

// --- 3. privilege escalation is impossible through registration -------------

func TestRegisterRejectsRoleFieldsInBody(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	// Strict decoding means an attempt to smuggle a role is refused outright
	// rather than quietly dropped, so the attempt is visible in the audit trail.
	bodies := []string{
		`{"username":"attacker","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate","roles":["super_admin"]}`,
		`{"username":"attacker","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate","role":"super_admin"}`,
		`{"username":"attacker","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate","status":1}`,
		`{"username":"attacker","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate","is_admin":true}`,
		`{"username":"attacker","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate","auth_version":99}`,
		`{"username":"attacker","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate","permission":"super_admin"}`,
		`{"username":"attacker","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate","permissions":["super_admin"]}`,
	}
	for _, body := range bodies {
		rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register", body, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d: %s", body, rec.Code, rec.Body.String())
		}
	}
	if _, err := store.FindAdminUserByUsername(context.Background(), "attacker"); !errors.Is(err, storage.ErrAdminUserNotFound) {
		t.Fatal("no account may be created by a request carrying role fields")
	}
}

func TestRegisterRoleListIsFixedRegardlessOfInput(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("zhangsan", "张三", "Campus#2026Gate"), nil)

	user, err := store.FindAdminUserByUsername(context.Background(), "zhangsan")
	if err != nil {
		t.Fatal(err)
	}
	if hasManagementRole(user.Roles) {
		t.Fatalf("public registration must never produce a management role, got %v", user.Roles)
	}
}

// --- 4. input validation ---------------------------------------------------

func TestRegisterRejectsDuplicateUsernameCaseInsensitively(t *testing.T) {
	s, _, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	seedMember(t, storeOf(s), "ZhangSan", "Campus#2026Gate")

	rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("zhangsan", "张三", "Campus#2026Gate"), nil)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "username_taken" {
		t.Fatalf("expected username_taken, got %q", code)
	}
}

func TestRegisterRejectsWeakAndMismatchedPasswords(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	cases := []struct {
		name string
		body string
	}{
		{"too short", `{"username":"a1","password":"Ab#12345","confirm_password":"Ab#12345"}`},
		{"single character class", `{"username":"a2","password":"aaaaaaaaaaaa","confirm_password":"aaaaaaaaaaaa"}`},
		{"equals username", `{"username":"samepass","password":"samepass","confirm_password":"samepass"}`},
		{"confirmation mismatch", `{"username":"a3","password":"Campus#2026Gate","confirm_password":"Campus#2027Gate"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register", tc.body, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}

	// Only the setup administrator should exist.
	if len(store.users) != 1 {
		t.Fatalf("invalid registrations must not create accounts, got %d", len(store.users))
	}
}

// --- 5. availability and throttling ---------------------------------------

// P1-03. A refused registration must cost nothing: no account, no session and
// no unit of the abuse budget. Charging the limiter before the switch is
// consulted would let anyone drain another visitor's quota by posting to a
// closed endpoint.
func TestRegisterRefusedWhenDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.PublicRegistrationEnabled = false
	s, store, reg := newTestServerWithRegistration(t, cfg)
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("zhangsan", "张三", "Campus#2026Gate"), nil)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "registration_disabled" {
		t.Fatalf("expected registration_disabled, got %q", code)
	}
	if len(store.users) != 1 {
		t.Fatalf("a refused registration must not create an account, got %d users", len(store.users))
	}
	if store.liveSessions() != 0 {
		t.Fatalf("a refused registration must not open a session, got %d", store.liveSessions())
	}
	if reg.consumed() != 0 {
		t.Fatalf("a refused registration must not consume the abuse budget, got %d", reg.consumed())
	}
}

func TestRegisterDoesNotExistOutsideSessionMode(t *testing.T) {
	for _, mode := range []string{"trusted-header", "token"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig()
			cfg.AdminAuthMode = mode
			s, _, _ := newTestServerWithRegistration(t, cfg)

			rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
				registerBody("zhangsan", "张三", "Campus#2026Gate"), nil)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected 404 in %s mode, got %d", mode, rec.Code)
			}
		})
	}
}

func TestRegisterIsRateLimitedPerAddress(t *testing.T) {
	cfg := testConfig()
	cfg.PublicRegisterMaxAttempts = 2
	s, _, _ := newTestServerWithRegistration(t, cfg)
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	for i, name := range []string{"user-one", "user-two"} {
		rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
			registerBody(name, name, "Campus#2026Gate"), nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("attempt %d: expected 201, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}

	rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("user-three", "user-three", "Campus#2026Gate"), nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "registration_rate_limited" {
		t.Fatalf("expected registration_rate_limited, got %q", code)
	}
}

// A failed attempt still costs budget: otherwise a script could probe for
// existing usernames at full speed by always choosing a name that is taken.
func TestRegisterFailedAttemptsStillConsumeBudget(t *testing.T) {
	cfg := testConfig()
	cfg.PublicRegisterMaxAttempts = 2
	s, _, _ := newTestServerWithRegistration(t, cfg)
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	seedMember(t, storeOf(s), "taken", "Campus#2026Gate")

	for i := 0; i < 2; i++ {
		rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
			registerBody("taken", "taken", "Campus#2026Gate"), nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("attempt %d: expected 409, got %d", i+1, rec.Code)
		}
	}
	rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("fresh", "fresh", "Campus#2026Gate"), nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a taken username must still consume budget; got %d", rec.Code)
	}
}

// Registration fails closed, unlike login throttling. If the counter cannot be
// consulted, unlimited signups would be exactly the wrong answer.
func TestRegisterFailsClosedWhenCounterUnavailable(t *testing.T) {
	s, store, reg := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	reg.broken = true

	rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("zhangsan", "张三", "Campus#2026Gate"), nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "registration_unavailable" {
		t.Fatalf("expected registration_unavailable, got %q", code)
	}
	if len(store.users) != 1 {
		t.Fatalf("no account may be created while the counter is unreachable, got %d", len(store.users))
	}
}

// A refused attempt must not hand the caller a fresh window. If a denial reset
// the counter, an attacker could keep an address permanently at the limit and
// still register on every other attempt.
func TestRepeatedDeniedAttemptsDoNotResetTheWindow(t *testing.T) {
	cfg := testConfig()
	cfg.PublicRegisterMaxAttempts = 2
	s, _, _ := newTestServerWithRegistration(t, cfg)
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	for i, name := range []string{"user-one", "user-two"} {
		if rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
			registerBody(name, name, "Campus#2026Gate"), nil); rec.Code != http.StatusCreated {
			t.Fatalf("attempt %d: expected 201, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}

	// Every further attempt stays refused, however many of them there are.
	for i := 0; i < 5; i++ {
		rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
			registerBody("user-extra", "user-extra", "Campus#2026Gate"), nil)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("denied attempt %d returned %d, want 429: %s", i+1, rec.Code, rec.Body.String())
		}
		if code := errorCode(t, rec); code != "registration_rate_limited" {
			t.Fatalf("expected registration_rate_limited, got %q", code)
		}
	}
}

// The budget is charged after the cheap validation and before the account is
// created, which is what makes these four cases free and the duplicate-username
// case expensive.
func TestMalformedRequestsDoNotConsumeBudget(t *testing.T) {
	cfg := testConfig()
	cfg.PublicRegisterMaxAttempts = 2
	s, store, reg := newTestServerWithRegistration(t, cfg)
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	cases := []struct {
		name string
		body string
	}{
		{"malformed json", `{"username":`},
		{"weak password", `{"username":"a1","password":"aaaaaaaaaaaa","confirm_password":"aaaaaaaaaaaa"}`},
		{"mismatched confirmation", `{"username":"a2","password":"Campus#2026Gate","confirm_password":"Campus#2027Gate"}`},
		{"over-long username", `{"username":"` + strings.Repeat("u", 65) + `","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate"}`},
		{"unknown security field", `{"username":"a3","password":"Campus#2026Gate","confirm_password":"Campus#2026Gate","roles":["super_admin"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register", tc.body, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
	if got := reg.consumed(); got != 0 {
		t.Fatalf("%d units of budget charged for requests that never reached account creation", got)
	}
	if len(store.users) != 1 {
		t.Fatalf("expected only the setup administrator, got %d accounts", len(store.users))
	}

	// A duplicate username, by contrast, is a well-formed attempt: it must cost
	// budget, or the endpoint could be used to enumerate existing names for free.
	seedMember(t, store, "taken", "Campus#2026Gate")
	if rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("taken", "taken", "Campus#2026Gate"), nil); rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
	if got := reg.consumed(); got != 1 {
		t.Fatalf("a duplicate username charged %d units of budget, want 1", got)
	}
}

// --- 6. member RBAC --------------------------------------------------------

func TestMemberSelfServiceEndpointsAreAllowed(t *testing.T) {
	cases := []struct{ method, path string }{
		{http.MethodGet, "/admin/whoami"},
		{http.MethodPost, "/admin/password"},
		{http.MethodGet, "/admin/sessions"},
		{http.MethodPost, "/admin/sessions/abc/revoke"},
		{http.MethodPost, "/admin/auth/logout"},
		{http.MethodPost, "/admin/logout"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		p := adminPrincipal{UserID: "u1", User: "zhangsan", Roles: map[string]bool{RoleMember: true}}
		if !p.can(req) {
			t.Fatalf("member must be allowed %s %s", tc.method, tc.path)
		}
	}
}

func TestMemberDeniedEveryManagementEndpoint(t *testing.T) {
	cases := []struct{ method, path string }{
		{http.MethodGet, "/admin/status"},
		{http.MethodGet, "/admin/providers"},
		{http.MethodPost, "/admin/providers"},
		{http.MethodGet, "/admin/models"},
		{http.MethodPost, "/admin/models/x/routes"},
		{http.MethodPut, "/admin/model-routes/x"},
		{http.MethodPost, "/admin/providers"},
		{http.MethodPost, "/admin/model-test"},
		{http.MethodGet, "/admin/tenants"},
		{http.MethodGet, "/admin/api-keys"},
		{http.MethodGet, "/admin/request-logs"},
		{http.MethodGet, "/admin/admin-audit"},
		{http.MethodGet, "/admin/security-events"},
		{http.MethodGet, "/admin/users"},
		{http.MethodPatch, "/admin/users/x"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		p := adminPrincipal{UserID: "u1", User: "zhangsan", Roles: map[string]bool{RoleMember: true}}
		if p.can(req) {
			t.Fatalf("member must be denied %s %s", tc.method, tc.path)
		}
	}
}

// The same matrix, driven through the real middleware with a real session, so
// the answer comes from the whole chain rather than from one function.
func TestMemberSessionIsRejectedByTheMiddleware(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	seedMember(t, store, "zhangsan", "Campus#2026Gate")
	token := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")

	allowed := []struct{ method, path string }{
		{http.MethodGet, "/admin/whoami"},
		{http.MethodGet, "/admin/sessions"},
	}
	for _, tc := range allowed {
		if rec := callThroughAdminMiddleware(t, s, token, tc.method, tc.path, ""); rec.Code != http.StatusOK {
			t.Fatalf("member %s %s should pass the middleware, got %d", tc.method, tc.path, rec.Code)
		}
	}

	denied := []struct{ method, path string }{
		{http.MethodGet, "/admin/status"},
		{http.MethodGet, "/admin/providers"},
		{http.MethodGet, "/admin/models"},
		{http.MethodGet, "/admin/tenants"},
		{http.MethodGet, "/admin/api-keys"},
		{http.MethodGet, "/admin/request-logs"},
		{http.MethodGet, "/admin/admin-audit"},
		{http.MethodGet, "/admin/security-events"},
		{http.MethodGet, "/admin/users"},
		{http.MethodPost, "/admin/providers"},
		{http.MethodPost, "/admin/model-test"},
		{http.MethodGet, "/admin/model-test-options"},
	}
	for _, tc := range denied {
		rec := callThroughAdminMiddleware(t, s, token, tc.method, tc.path, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("member %s %s must be 403, got %d: %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

// --- 7. user management ----------------------------------------------------

func TestOnlySuperAdminReachesUserManagement(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	for _, role := range []string{RoleGatewayAdmin, RoleTenantAdmin, RoleAuditor, RoleOperator, RoleMember} {
		t.Run(role, func(t *testing.T) {
			name := "user-" + role
			hash, err := security.HashPassword("Campus#2026Gate")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateUser(context.Background(), name, name, hash, []string{role}); err != nil {
				t.Fatal(err)
			}
			token := accessTokenFor(t, s, name, "Campus#2026Gate")

			for _, tc := range []struct{ method, path string }{
				{http.MethodGet, "/admin/users"},
				{http.MethodPatch, "/admin/users/00000000-0000-0000-0000-000000000000"},
			} {
				rec := callThroughAdminMiddleware(t, s, token, tc.method, tc.path, `{"status":2}`)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("%s %s as %s must be 403, got %d", tc.method, tc.path, role, rec.Code)
				}
			}
		})
	}
}

func TestSuperAdminListsUsersWithoutCredentialFields(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	admin := seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	seedMember(t, store, "zhangsan", "Campus#2026Gate")

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req = req.WithContext(withAdminPrincipal(req.Context(), adminPrincipal{
		UserID: admin.ID, User: admin.Username, Roles: map[string]bool{RoleSuperAdmin: true},
	}))
	rec := httptest.NewRecorder()
	s.handleAdminUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	for _, forbidden := range []string{"password_hash", "refresh_hash", "auth_version", "authVersion"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("the user list must not expose %s: %s", forbidden, raw)
		}
	}

	body := decodeBody(t, rec)
	data, ok := body["data"].([]any)
	if !ok || len(data) != 2 {
		t.Fatalf("expected 2 accounts, got %v", body["data"])
	}
	stats, ok := body["stats"].(map[string]any)
	if !ok {
		t.Fatalf("expected stats in the response, got %v", body["stats"])
	}
	if stats["total"] != float64(2) || stats["members"] != float64(1) || stats["admins"] != float64(1) {
		t.Fatalf("unexpected stats: %v", stats)
	}
}

func TestPromotionBumpsAuthVersionAndRevokesSessions(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")

	// The member is signed in on two devices.
	before := member.AuthVersion
	accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")
	accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")
	if store.liveSessions() != 2 {
		t.Fatalf("expected 2 live sessions, got %d", store.liveSessions())
	}

	roles := []string{RoleGatewayAdmin}
	updated, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Roles: &roles})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(updated.Roles) != 1 || updated.Roles[0] != RoleGatewayAdmin {
		t.Fatalf("expected [gateway_admin], got %v", updated.Roles)
	}
	if updated.AuthVersion != before+1 {
		t.Fatalf("auth_version must advance: %d -> %d", before, updated.AuthVersion)
	}
	if store.liveSessions() != 0 {
		t.Fatalf("role change must revoke every session of the account, %d live", store.liveSessions())
	}
}

func TestPromotedUserSignsInWithTheNewRole(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")

	roles := []string{RoleGatewayAdmin}
	if _, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Roles: &roles}); err != nil {
		t.Fatalf("promote: %v", err)
	}

	token := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")
	rec := callThroughAdminMiddleware(t, s, token, http.MethodGet, "/admin/providers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("a promoted account must reach the supplier API, got %d: %s", rec.Code, rec.Body.String())
	}
	// …and must still be denied the user management API it does not own.
	rec = callThroughAdminMiddleware(t, s, token, http.MethodGet, "/admin/users", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("gateway_admin must not reach /admin/users, got %d", rec.Code)
	}
}

func TestDemotionReturnsTheAccountToMember(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")
	// The administrator keeps a session throughout, so "only the demoted
	// account is signed out" is a real assertion rather than a vacuous one.
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	roles := []string{RoleGatewayAdmin}
	if _, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Roles: &roles}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")

	back := []string{RoleMember}
	updated, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Roles: &back})
	if err != nil {
		t.Fatalf("demote: %v", err)
	}
	if len(updated.Roles) != 1 || updated.Roles[0] != RoleMember {
		t.Fatalf("expected [member], got %v", updated.Roles)
	}
	if store.liveSessions() != 1 {
		t.Fatalf("only the demoted account may be signed out; %d sessions live", store.liveSessions())
	}
	if rec := callThroughAdminMiddleware(t, s, adminToken, http.MethodGet, "/admin/users", ""); rec.Code != http.StatusOK {
		t.Fatalf("the administrator's own session must survive the demotion, got %d", rec.Code)
	}

	token := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")
	if rec := callThroughAdminMiddleware(t, s, token, http.MethodGet, "/admin/providers", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("a demoted account must lose management access, got %d", rec.Code)
	}
}

func TestRoleChangeNormalisesMemberAway(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")

	roles := []string{RoleMember, RoleGatewayAdmin}
	updated, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Roles: &roles})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(updated.Roles) != 1 || updated.Roles[0] != RoleGatewayAdmin {
		t.Fatalf("member must be dropped alongside a management role, got %v", updated.Roles)
	}
}

func TestDisplayNameChangeKeepsSessions(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")
	accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")

	name := "张三"
	updated, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{DisplayName: &name})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if updated.DisplayName != name {
		t.Fatalf("display name not applied: %q", updated.DisplayName)
	}
	if updated.AuthVersion != member.AuthVersion {
		t.Fatal("a cosmetic change must not advance auth_version")
	}
	if store.liveSessions() != 1 {
		t.Fatalf("a cosmetic change must not sign anyone out, %d live", store.liveSessions())
	}
}

// --- 8. last super administrator protection --------------------------------

// The invariant is enforced inside the writing transaction, so it holds even
// when the change is the operator's own account — the case where a slip would
// leave the deployment with nobody able to administer it.
func TestLoneSuperAdminCannotStepDown(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	admin := seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	roles := []string{RoleMember}
	if _, _, err := s.adminAuth.updateUser(context.Background(), admin.ID, userUpdate{Roles: &roles}); !errors.Is(err, ErrAdminLastSuperAdmin) {
		t.Fatalf("expected ErrAdminLastSuperAdmin, got %v", err)
	}
	if u, _ := store.FindAdminUserByID(context.Background(), admin.ID); !hasSuperAdmin(u.Roles) {
		t.Fatal("the refused change must leave the role list alone")
	}
}

func TestLoneSuperAdminCannotBeDisabled(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	admin := seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	disabled := model.AdminStatusDisabled
	if _, _, err := s.adminAuth.updateUser(context.Background(), admin.ID, userUpdate{Status: &disabled}); !errors.Is(err, ErrAdminLastSuperAdmin) {
		t.Fatalf("expected ErrAdminLastSuperAdmin, got %v", err)
	}
	if u, _ := store.FindAdminUserByID(context.Background(), admin.ID); u.Status != model.AdminStatusEnabled {
		t.Fatal("the refused change must leave the status alone")
	}
}

// With two super administrators, one may step down — that is what makes it
// possible to hand the role over without ever leaving the system unmanaged.
func TestSecondSuperAdminAllowsOneToStepDown(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	admin := seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	second := seedSetupAdminViaStore(t, store, "second-admin")

	roles := []string{RoleGatewayAdmin}
	updated, _, err := s.adminAuth.updateUser(context.Background(), admin.ID, userUpdate{Roles: &roles})
	if err != nil {
		t.Fatalf("demote with a second super admin present: %v", err)
	}
	if hasSuperAdmin(updated.Roles) {
		t.Fatalf("expected the role to be gone, got %v", updated.Roles)
	}

	// Only one remains now, and it can no longer remove itself.
	back := []string{RoleMember}
	if _, _, err := s.adminAuth.updateUser(context.Background(), second.ID, userUpdate{Roles: &back}); !errors.Is(err, ErrAdminLastSuperAdmin) {
		t.Fatalf("expected ErrAdminLastSuperAdmin once only one remains, got %v", err)
	}
}

// A disabled super administrator does not count towards the invariant: it could
// not sign in to administer anything.
func TestDisabledSuperAdminDoesNotSatisfyTheInvariant(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	admin := seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	second := seedSetupAdminViaStore(t, store, "second-admin")

	disabled := model.AdminStatusDisabled
	if _, _, err := s.adminAuth.updateUser(context.Background(), second.ID, userUpdate{Status: &disabled}); err != nil {
		t.Fatalf("disabling a second super admin must be allowed: %v", err)
	}
	// The disabled account may not be re-counted to justify stepping down.
	if _, _, err := s.adminAuth.updateUser(context.Background(), admin.ID, userUpdate{Status: &disabled}); !errors.Is(err, ErrAdminLastSuperAdmin) {
		t.Fatalf("expected ErrAdminLastSuperAdmin, got %v", err)
	}
}

// The same rule through the HTTP endpoint, so the status code the console sees
// is pinned as well.
func TestLastSuperAdminGuardReturnsConflict(t *testing.T) {
	s, _, _ := newTestServerWithRegistration(t, testConfig())
	admin := seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	token := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	req := httptest.NewRequest(http.MethodPatch, "/admin/users/"+admin.ID,
		strings.NewReader(`{"roles":["member"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "test-req")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	s.adminMiddleware(http.HandlerFunc(s.handleAdminUserByID)).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "last_super_admin" {
		t.Fatalf("expected last_super_admin, got %q", code)
	}
}

// --- 9. disable and re-enable ---------------------------------------------

func TestDisableRevokesSessionsAndBlocksLogin(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")
	accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")
	token := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")

	disabled := model.AdminStatusDisabled
	if _, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Status: &disabled}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if store.liveSessions() != 1 {
		t.Fatalf("only the disabled account's sessions may be revoked, %d live", store.liveSessions())
	}
	// Disabling revokes the account's sessions, so the next request is answered
	// as a revoked session (401) rather than as a disabled account (403). The
	// 403 is what a login attempt gets, below.
	rec := callThroughAdminMiddleware(t, s, token, http.MethodGet, "/admin/whoami", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled account's token must stop working, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "session_revoked" {
		t.Fatalf("expected session_revoked, got %q", code)
	}
	if _, err := s.adminAuth.login(context.Background(), "zhangsan", "Campus#2026Gate", "10.0.0.9", "ua"); !errors.Is(err, ErrAdminUserDisabled) {
		t.Fatalf("expected ErrAdminUserDisabled on login, got %v", err)
	}
}

func TestReEnableRestoresLogin(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")

	disabled := model.AdminStatusDisabled
	if _, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Status: &disabled}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	enabled := model.AdminStatusEnabled
	if _, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Status: &enabled}); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if _, err := s.adminAuth.login(context.Background(), "zhangsan", "Campus#2026Gate", "10.0.0.9", "ua"); err != nil {
		t.Fatalf("a re-enabled account must be able to sign in: %v", err)
	}
}

func TestUpdateUnknownUserIsNotFound(t *testing.T) {
	s, _, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	roles := []string{RoleMember}
	if _, _, err := s.adminAuth.updateUser(context.Background(), "00000000-0000-0000-0000-000000000000", userUpdate{Roles: &roles}); !errors.Is(err, ErrAdminUserNotFound) {
		t.Fatalf("expected ErrAdminUserNotFound, got %v", err)
	}
}

// --- 9b. user management is a session-mode feature ---------------------------

// Token and trusted-header mode have no account service behind them: accounts
// come from the campus identity platform. Reaching for that nil service used to
// panic the request, so this pins the refusal.
func TestUserManagementRefusedOutsideSessionMode(t *testing.T) {
	for _, mode := range []string{"token", "trusted-header"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig()
			cfg.AdminAuthMode = mode
			cfg.AdminToken = "break-glass-token"
			cfg.AdminProxySecret = "proxy-secret"
			s, _, _ := newTestServerWithRegistration(t, cfg)

			req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
			rec := httptest.NewRecorder()
			s.handleAdminUsers(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("GET /admin/users in %s mode: expected 503, got %d: %s", mode, rec.Code, rec.Body.String())
			}
			if code := errorCode(t, rec); code != "admin_api_disabled" {
				t.Fatalf("expected admin_api_disabled, got %q", code)
			}

			patch := httptest.NewRequest(http.MethodPatch, "/admin/users/abc", strings.NewReader(`{"roles":["member"]}`))
			patch.Header.Set("Content-Type", "application/json")
			rec = httptest.NewRecorder()
			s.handleAdminUserByID(rec, patch)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("PATCH /admin/users/{id} in %s mode: expected 503, got %d: %s", mode, rec.Code, rec.Body.String())
			}
		})
	}
}

// --- 10. regression: the session layer is unchanged -------------------------

func TestMemberRefreshRotationStillWorks(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	seedMember(t, store, "zhangsan", "Campus#2026Gate")

	tokens, err := s.adminAuth.login(context.Background(), "zhangsan", "Campus#2026Gate", "10.0.0.9", "ua")
	if err != nil {
		t.Fatalf("member login: %v", err)
	}
	rotated, err := s.adminAuth.refresh(context.Background(), tokens.RefreshToken, "10.0.0.9", "ua")
	if err != nil {
		t.Fatalf("member refresh: %v", err)
	}
	if rotated.RefreshToken == tokens.RefreshToken {
		t.Fatal("the refresh token must rotate")
	}
	// A benign concurrent refresh must still be accepted for a member.
	if _, err := s.adminAuth.refresh(context.Background(), tokens.RefreshToken, "10.0.0.9", "ua"); err != nil {
		t.Fatalf("benign concurrent refresh must be accepted: %v", err)
	}
	if store.liveSessions() != 1 {
		t.Fatalf("the session must survive a benign race, %d live", store.liveSessions())
	}
}

// Phase 1 removed the auditor fallback for the trusted-header path.
//
// This test used to pin the opposite behaviour — an empty or unmappable proxy
// group list resolved to auditor, which reads request logs, the management
// audit trail and security events. That handed an authenticated but unmapped
// identity visibility nobody granted, so the handoff revokes it: a group list
// that maps to no global role now resolves to member, the least privilege. The
// test now pins the confirmed behaviour so the regression cannot come back.
func TestTrustedHeaderRolelessFallbackIsMember(t *testing.T) {
	roles := parseAdminRoles("")
	if len(roles) != 1 || !roles[RoleMember] {
		t.Fatalf("an empty group list must resolve to member, got %v", roles)
	}
	if roles[RoleAuditor] {
		t.Fatalf("an unmapped identity must never become an auditor, got %v", roles)
	}
	// The campus proxy has no "member" concept, but it may send the word; the
	// parser understands it either way.
	if r := parseAdminRoles("member"); len(r) != 1 || !r[RoleMember] {
		t.Fatalf("the parser must understand member, got %v", r)
	}
	// The whole header is normalised as one list: a management role makes the
	// account an administrator, so member is dropped rather than kept beside it.
	if r := parseAdminRoles("gateway_admin, member"); !r[RoleGatewayAdmin] || r[RoleMember] {
		t.Fatalf("a management role beside member must drop member, got %v", r)
	}
}

// P1-07. The account is created first and the session second, so the second
// write can fail on its own — the store was reachable a millisecond ago and is
// not now. The old implementation returned a generic internal error, which
// told the user their registration had failed; their retry was answered
// "username taken" with nothing to explain it, and the account they had
// successfully created was unreachable for them.
//
// The contract now is: 503 account_created_login_failed, the account stays,
// and the user proceeds straight to login. This test fails on the old
// implementation, which answered 500 admin_internal_error.
func TestRegisterAccountCreatedButSessionFailed(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	store.createSessionErr = errors.New("session store unavailable")

	rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("zhangsan", "张三", "Campus#2026Gate"), nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "account_created_login_failed" {
		t.Fatalf("expected account_created_login_failed, got %q", code)
	}

	// The account must exist exactly once — a retry is never issued, and a
	// compensating delete is not part of the contract.
	if len(store.users) != 2 {
		t.Fatalf("the account must be kept, got %d users", len(store.users))
	}
	if store.liveSessions() != 0 {
		t.Fatalf("no session may be opened, got %d", store.liveSessions())
	}

	// The response is a recovery instruction, not a credential echo.
	raw := rec.Body.String()
	if strings.Contains(raw, "Campus#2026Gate") || strings.Contains(raw, "access_token") {
		t.Fatalf("the response leaked credential material: %s", raw)
	}

	// The user can immediately log in with the password they chose.
	login := postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login",
		`{"username":"zhangsan","password":"Campus#2026Gate"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("the new account must be usable straight away, got %d: %s", login.Code, login.Body.String())
	}
}

// The audit trail must be able to tell the three outcomes apart: a plain
// failure, the partial success, and the complete success (audited by the
// handler as account_registered). The mapping lives in one pure function so
// this holds without a database.
func TestRegistrationPartialFailureIsAuditedDistinctly(t *testing.T) {
	if got := registrationAuditAction(ErrAdminAccountCreatedLoginFailed); got != "account_created_login_failed" {
		t.Fatalf("partial success audit action = %q, want account_created_login_failed", got)
	}
	for _, err := range []error{ErrAdminInternal, errors.New("anything unexpected")} {
		if got := registrationAuditAction(err); got != "account_register_failed" {
			t.Fatalf("unexpected error %v audited as %q, want account_register_failed", err, got)
		}
	}
	if got := registrationAuditAction(ErrAdminUsernameTaken); got == "account_created_login_failed" {
		t.Fatal("a username conflict must not be reported as a partial success")
	}
	// The status mapping is what the client sees; it must stay a 503 with the
	// stable code the frontend switches on.
	if status, code := adminErrorStatus(ErrAdminAccountCreatedLoginFailed); status != http.StatusServiceUnavailable || code != "account_created_login_failed" {
		t.Fatalf("adminErrorStatus = %d/%q, want 503/account_created_login_failed", status, code)
	}
}

// P2-4 (review). The account is created first and the session second, so a
// failure between them is possible — and the minting that follows the session
// insert is the one step whose failure would leave a committed session nobody
// holds a token for. The old code left it live: the account's device list
// showed a device that never existed, until the refresh TTL expired.
//
// This injects the exact interleaving — session committed, mint fails — and
// asserts the row is retired with its own reason while the account survives.
func TestRegisterDoesNotLeaveAnOrphanedSession(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	s.adminAuth.mint = func(model.AdminUser, model.AdminSession) (string, time.Time, error) {
		return "", time.Time{}, errors.New("token minting unavailable")
	}

	rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("zhangsan", "张三", "Campus#2026Gate"), nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "account_created_login_failed" {
		t.Fatalf("expected account_created_login_failed, got %q", code)
	}
	if store.liveSessions() != 0 {
		t.Fatal("a failed automatic sign-in must not leave a live session nobody holds a token for")
	}
	for _, sess := range store.sessions {
		if sess.RevokeReason != revokeReasonSignInFailed {
			t.Fatalf("the stranded session must be retired as %q, got %q", revokeReasonSignInFailed, sess.RevokeReason)
		}
	}
	// The account survives: the user's next step is to log in with the
	// password they chose, which is exactly what the 503 tells them.
	if _, err := store.FindAdminUserByUsername(context.Background(), "zhangsan"); err != nil {
		t.Fatalf("the account must survive: %v", err)
	}
}

// The refresh path must NOT retire the session when minting fails there: the
// rotation has committed, the client still holds the superseded token, and a
// retry inside the grace window succeeds. Revoking would turn a recoverable
// race into a forced re-login.
func TestRefreshMintFailureKeepsTheSession(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "campus-admin", "Campus#2026Gate")
	tokens, err := svc.login(context.Background(), "campus-admin", "Campus#2026Gate", "10.0.0.5", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	svc.mint = func(model.AdminUser, model.AdminSession) (string, time.Time, error) {
		return "", time.Time{}, errors.New("token minting unavailable")
	}
	if _, err := svc.refresh(context.Background(), tokens.RefreshToken, "10.0.0.5", ""); !errors.Is(err, ErrAdminInternal) {
		t.Fatalf("expected ErrAdminInternal, got %v", err)
	}
	if store.sessions[tokens.Session.SID].RevokedAt != nil {
		t.Fatal("a mint failure after rotation must not revoke the session: the client retries inside the grace window")
	}
}

// --- 11. registration records an account event ------------------------------

func TestRegistrationIsAuditedWithoutCredentials(t *testing.T) {
	s, _, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	rec := postJSON(t, http.HandlerFunc(s.handleAccountRegister), http.MethodPost, "/auth/register",
		registerBody("zhangsan", "张三", "Campus#2026Gate"), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	// The response must not echo the credential back beyond the access token
	// the client needs.
	raw := rec.Body.String()
	if strings.Contains(raw, "Campus#2026Gate") || strings.Contains(raw, "password_hash") {
		t.Fatalf("the registration response leaked credential material: %s", raw)
	}
}

// --- helpers ---------------------------------------------------------------

// storeOf reaches the fake store behind a server built by the shared helper.
func storeOf(s *Server) *fakeAdminStore {
	return s.adminAuth.store.(*fakeAdminStore)
}

// seedSetupAdminViaStore inserts an additional super administrator. It goes
// through CreateUser because CreateFirstAdminUser is closed once one exists.
func seedSetupAdminViaStore(t *testing.T, store *fakeAdminStore, username string) model.AdminUser {
	t.Helper()
	hash, err := security.HashPassword("Campus#2026Gate")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(context.Background(), username, username, hash, []string{RoleSuperAdmin})
	if err != nil {
		t.Fatalf("seed super admin: %v", err)
	}
	return u
}
