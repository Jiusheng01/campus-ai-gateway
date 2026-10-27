package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
)

// resetPasswordVia calls the reset endpoint through the real middleware, so the
// request is authorised exactly as a browser's would be.
func resetPasswordVia(t *testing.T, s *Server, token, targetID, newPassword string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"new_password":"` + newPassword + `"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetID+"/password-reset", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Request-Id", "test-req")
	rec := httptest.NewRecorder()
	s.adminMiddleware(http.HandlerFunc(s.handleAdminUserByID)).ServeHTTP(rec, req)
	return rec
}

// callAs drives a request through the management middleware as the given
// session's holder. It is the probe every fence test uses: what matters is the
// status the middleware returns, not what any one handler would have done.
//
// The inner handler answers 200 without doing anything. That is deliberate: the
// probe asserts the middleware's decision, so keeping the handler inert means a
// 200 can only come from "the middleware allowed it", not from whatever the page
// behind it happened to return.
func callAs(t *testing.T, s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "test-req")
	rec := httptest.NewRecorder()
	s.adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec
}

// callAdminRoute drives a request through the middleware and the account
// handlers. Used where the assertion is about what a handler produced, not only
// about whether it was reached. /admin/users is served by its own handlers
// rather than by the management router, so this helper mirrors that split.
func callAdminRoute(t *testing.T, s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	inner := http.HandlerFunc(s.handleAdminUsers)
	if strings.HasPrefix(path, "/admin/users/") {
		inner = s.handleAdminUserByID
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "test-req")
	rec := httptest.NewRecorder()
	s.adminMiddleware(inner).ServeHTTP(rec, req)
	return rec
}

// callConsoleAs drives a personal-workspace request through its own middleware.
func callConsoleAs(t *testing.T, s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "test-req")
	rec := httptest.NewRecorder()
	s.consoleMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec
}

func TestAdminResetPasswordReplacesTheCredentialAndFencesTheAccount(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "OldCampus#2026")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	// The member is signed in somewhere when the operator resets the account.
	memberToken := accessTokenFor(t, s, "zhangsan", "OldCampus#2026")

	rec := resetPasswordVia(t, s, adminToken, member.ID, "Interim#2026Reset")
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	updated, err := store.FindAdminUserByID(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.PasswordResetRequired {
		t.Fatal("the reset must mark the account as requiring a new password")
	}
	if updated.AuthVersion != member.AuthVersion+1 {
		t.Fatalf("the reset must bump auth_version once: %d -> %d", member.AuthVersion, updated.AuthVersion)
	}
	if ok, _, _ := security.VerifyPassword(updated.PasswordHash, "Interim#2026Reset"); !ok {
		t.Fatal("the handed-over password must be the stored credential")
	}

	// The device that was signed in under the old password is gone, not merely
	// stale: the token it holds must not be accepted.
	if got := callAs(t, s, memberToken, http.MethodGet, "/admin/whoami", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("a session opened before the reset must stop working, got %d: %s", got.Code, got.Body.String())
	}

	// Signing in with the handed-over password works, but the session it opens
	// may only do the three things needed to replace it.
	resetToken := accessTokenFor(t, s, "zhangsan", "Interim#2026Reset")
	if got := callAs(t, s, resetToken, http.MethodGet, "/admin/whoami", ""); got.Code != http.StatusOK {
		t.Fatalf("whoami must be reachable while the reset is outstanding, got %d", got.Code)
	}
	for _, probe := range []struct{ method, path, body string }{
		{http.MethodGet, "/admin/status", ""},
		{http.MethodGet, "/admin/request-logs", ""},
		{http.MethodGet, "/admin/providers", ""},
		{http.MethodPatch, "/admin/users/" + member.ID, `{"display_name":"x"}`},
	} {
		got := callAs(t, s, resetToken, probe.method, probe.path, probe.body)
		if got.Code != http.StatusForbidden {
			t.Fatalf("%s %s while a reset is outstanding: expected 403, got %d: %s",
				probe.method, probe.path, got.Code, got.Body.String())
		}
		if code := errorCode(t, got); code != "password_reset_required" {
			t.Fatalf("%s %s must report password_reset_required, got %q", probe.method, probe.path, code)
		}
	}

	// The account sets its own password. That is the one write the fence admits,
	// and it also lifts the fence.
	change := postJSON(t, s.adminMiddleware(http.HandlerFunc(s.handleAdminPasswordChange)), http.MethodPost,
		"/admin/password", `{"current_password":"Interim#2026Reset","new_password":"Chosen#2026ByMe"}`,
		map[string]string{"Authorization": "Bearer " + resetToken})
	if change.Code != http.StatusOK {
		t.Fatalf("choosing a new password must work through the fence, got %d: %s", change.Code, change.Body.String())
	}

	after, err := store.FindAdminUserByID(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.PasswordResetRequired {
		t.Fatal("choosing a new password must clear the reset obligation")
	}

	// The reset session was revoked by the password change, as any password
	// change does; the account signs in with the password it chose and reaches
	// the console normally.
	finalToken := accessTokenFor(t, s, "zhangsan", "Chosen#2026ByMe")
	if got := callAs(t, s, finalToken, http.MethodGet, "/admin/whoami", ""); got.Code != http.StatusOK {
		t.Fatalf("the account must be usable after choosing its own password, got %d", got.Code)
	}
}

// A reset password must not survive as the account's permanent credential. This
// is the scenario that makes the fence load-bearing: without it, an operator who
// resets an account can sign in as that account and keep its roles.
func TestAdminResetPasswordDoesNotGiveTheOperatorTheAccount(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	operator := seedMember(t, store, "operator-two", "Campus#2026Gate")
	// A second administrator performs the reset. The point is that the
	// credential they choose is still not enough to act as the target.
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	if rec := resetPasswordVia(t, s, adminToken, operator.ID, "Interim#2026Reset"); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d: %s", rec.Code, rec.Body.String())
	}

	operatorToken := accessTokenFor(t, s, "operator-two", "Interim#2026Reset")
	got := callConsoleAs(t, s, operatorToken, http.MethodPost, "/api/console/keys", `{}`)
	if got.Code != http.StatusForbidden {
		t.Fatalf("the personal workspace must be closed while a reset is outstanding, got %d: %s",
			got.Code, got.Body.String())
	}
}

func TestAdminResetPasswordRefusesADisabledAccount(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	// Disabling first is the honest order: a reset on a disabled account would
	// look like restored access while sign-in still refused.
	rec := callAdminRoute(t, s, adminToken, http.MethodPatch, "/admin/users/"+member.ID,
		`{"status":`+strconv.Itoa(model.AdminStatusDisabled)+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d: %s", rec.Code, rec.Body.String())
	}

	got := resetPasswordVia(t, s, adminToken, member.ID, "Interim#2026Reset")
	if got.Code != http.StatusForbidden {
		t.Fatalf("a reset on a disabled account: expected 403, got %d: %s", got.Code, got.Body.String())
	}
	if code := errorCode(t, got); code != "account_disabled" {
		t.Fatalf("the refusal must name the reason, got %q", code)
	}
}

func TestAdminResetPasswordEnforcesThePasswordPolicy(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	for _, weak := range []string{"short1!", "onlylettershere", "zhangsan", ""} {
		got := resetPasswordVia(t, s, adminToken, member.ID, weak)
		if got.Code != http.StatusBadRequest {
			t.Fatalf("password %q: expected 400, got %d: %s", weak, got.Code, got.Body.String())
		}
	}

	// None of the refusals may have touched the stored credential.
	after, err := store.FindAdminUserByID(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.PasswordResetRequired || after.AuthVersion != member.AuthVersion {
		t.Fatal("a refused reset must leave the account exactly as it was")
	}
}

// Only a super administrator may reset someone else's password. The check has to
// be the real RBAC decision, not a handler-local one, so a future role grant
// cannot widen it by accident.
func TestAdminResetPasswordIsSuperAdminOnly(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	seedMember(t, store, "zhangsan", "Campus#2026Gate")
	other := seedMember(t, store, "lisi", "Campus#2026Gate")

	memberToken := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")
	got := resetPasswordVia(t, s, memberToken, other.ID, "Interim#2026Reset")
	if got.Code != http.StatusForbidden {
		t.Fatalf("a member must not reset another account: expected 403, got %d: %s", got.Code, got.Body.String())
	}

	after, err := store.FindAdminUserByID(context.Background(), other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.PasswordResetRequired {
		t.Fatal("a refused reset must not mark the account")
	}
}

// The operator sees that a handover was never completed. Without this the user
// list would show a reset as done even when the holder still has not chosen a
// password of their own.
func TestResetRequirementIsVisibleToTheOperator(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	if rec := resetPasswordVia(t, s, adminToken, member.ID, "Interim#2026Reset"); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d: %s", rec.Code, rec.Body.String())
	}

	list := callAdminRoute(t, s, adminToken, http.MethodGet, "/admin/users", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d: %s", list.Code, list.Body.String())
	}
	body := list.Body.String()
	if !strings.Contains(body, `"password_reset_required":true`) {
		t.Fatalf("the user list must report the outstanding reset: %s", body)
	}
	if strings.Contains(body, "Interim#2026Reset") {
		t.Fatal("the handed-over password must never appear in a response body")
	}
}

// The browser routes on whoami, so the fence is only usable if the identity
// endpoint reports the obligation. Without this field the page would render a
// workspace whose every request answers 403.
func TestWhoAmIReportsTheOutstandingReset(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	if rec := resetPasswordVia(t, s, adminToken, member.ID, "Interim#2026Reset"); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d: %s", rec.Code, rec.Body.String())
	}

	resetToken := accessTokenFor(t, s, "zhangsan", "Interim#2026Reset")
	rec := callAdminRoute(t, s, resetToken, http.MethodGet, "/admin/whoami", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("whoami: %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"password_reset_required":true`) {
		t.Fatalf("whoami must report the outstanding reset: %s", rec.Body.String())
	}

	// The administrator who performed it is not affected.
	adminWho := callAdminRoute(t, s, adminToken, http.MethodGet, "/admin/whoami", "")
	if !strings.Contains(adminWho.Body.String(), `"password_reset_required":false`) {
		t.Fatalf("an untouched account must report false: %s", adminWho.Body.String())
	}
}

// The sign-in response has to say the account is fenced. The browser seeds its
// identity from that response and skips whoami while an identity exists, so an
// absent flag would drop the account into a console that answers 403 on every
// request instead of the password screen.
func TestLoginResponseCarriesTheResetFlag(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	if rec := resetPasswordVia(t, s, adminToken, member.ID, "Interim#2026Reset"); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d: %s", rec.Code, rec.Body.String())
	}

	rec := postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login",
		`{"username":"zhangsan","password":"Interim#2026Reset"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login with the handed-over password: %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	user, _ := body["user"].(map[string]any)
	if user == nil {
		t.Fatalf("login response has no user object: %s", rec.Body.String())
	}
	if required, ok := user["password_reset_required"].(bool); !ok || !required {
		t.Fatalf("the sign-in response must flag the outstanding reset: %s", rec.Body.String())
	}

	// An untouched account reports false, so the client can rely on the field
	// being present rather than guessing from its absence.
	adminLogin := postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login",
		`{"username":"campus-admin","password":"Campus#2026Gate"}`, nil)
	adminBody := decodeBody(t, adminLogin)
	adminUser, _ := adminBody["user"].(map[string]any)
	if required, ok := adminUser["password_reset_required"].(bool); !ok || required {
		t.Fatalf("an untouched account must report false: %s", adminLogin.Body.String())
	}
}
