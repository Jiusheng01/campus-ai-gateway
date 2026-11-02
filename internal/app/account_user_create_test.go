package app

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
)

// createUserVia opens an account through the real middleware and the users
// handler, so the request is authorised exactly as a browser's would be.
func createUserVia(t *testing.T, s *Server, token, body string) (int, string) {
	t.Helper()
	rec := callAdminRoute(t, s, token, http.MethodPost, "/admin/users", body)
	return rec.Code, rec.Body.String()
}

func createUserBody(username, password, roles string) string {
	return `{"username":"` + username + `","display_name":"新建账号","password":"` + password +
		`","roles":` + roles + `}`
}

func TestAdminCreatesAnAccountWithItsRoles(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	code, body := createUserVia(t, s, adminToken,
		createUserBody("wangwu", "Interim#2026New", `["gateway_admin"]`))
	if code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", code, body)
	}

	created, err := store.FindAdminUserByUsername(context.Background(), "wangwu")
	if err != nil {
		t.Fatalf("the account must exist: %v", err)
	}
	if len(created.Roles) != 1 || created.Roles[0] != RoleGatewayAdmin {
		t.Fatalf("roles = %v, want [gateway_admin]", created.Roles)
	}
	if !created.PasswordResetRequired {
		t.Fatal("an administrator-chosen password must be marked reset-required")
	}
	if ok, _, _ := security.VerifyPassword(created.PasswordHash, "Interim#2026New"); !ok {
		t.Fatal("the handed-over password must be the stored credential")
	}
	// The response must not echo the credential back into the browser log.
	if strings.Contains(body, "Interim#2026New") {
		t.Fatal("the created account response must not carry the password")
	}
}

// The account exists to be handed over, so the first thing it must be able to
// do is nothing: sign in, then be held to choosing its own password.
func TestCreatedAccountIsHeldToChoosingItsOwnPassword(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	if code, body := createUserVia(t, s, adminToken,
		createUserBody("wangwu", "Interim#2026New", `["member"]`)); code != http.StatusCreated {
		t.Fatalf("create: %d: %s", code, body)
	}
	_ = store

	token := accessTokenFor(t, s, "wangwu", "Interim#2026New")
	if got := callAs(t, s, token, http.MethodGet, "/admin/status", ""); got.Code != http.StatusForbidden {
		t.Fatalf("a handed-over password must not reach the console, got %d: %s", got.Code, got.Body.String())
	}
	if code := errorCode(t, callAs(t, s, token, http.MethodGet, "/admin/status", "")); code != "password_reset_required" {
		t.Fatalf("the refusal must report password_reset_required, got %q", code)
	}

	// Choosing a password lifts it, exactly as after a reset.
	change := postJSON(t, s.adminMiddleware(http.HandlerFunc(s.handleAdminPasswordChange)), http.MethodPost,
		"/admin/password", `{"current_password":"Interim#2026New","new_password":"Chosen#2026ByMe"}`,
		map[string]string{"Authorization": "Bearer " + token})
	if change.Code != http.StatusOK {
		t.Fatalf("choosing a password: %d: %s", change.Code, change.Body.String())
	}
	final := accessTokenFor(t, s, "wangwu", "Chosen#2026ByMe")
	// /admin/whoami is the one read every signed-in account may make, so it is
	// what "usable again" is measured by. /admin/status is not: a member is
	// forbidden it by RBAC whether or not a reset is outstanding, and asserting
	// on that would be testing the role list instead of the fence.
	if got := callAs(t, s, final, http.MethodGet, "/admin/whoami", ""); got.Code != http.StatusOK {
		t.Fatalf("after choosing its own password the account must be usable, got %d", got.Code)
	}
}

func TestAdminCreateRefusesADuplicateUsername(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	seedMember(t, store, "zhangsan", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	// Case-insensitively, the same rule login and registration follow.
	code, _ := createUserVia(t, s, adminToken, createUserBody("ZhangSan", "Interim#2026New", `["member"]`))
	if code != http.StatusConflict {
		t.Fatalf("duplicate username: expected 409, got %d", code)
	}
}

func TestAdminCreateEnforcesThePasswordPolicy(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	for _, weak := range []string{"short1!", "onlylettershere", "wangwu", ""} {
		code, body := createUserVia(t, s, adminToken, createUserBody("wangwu", weak, `["member"]`))
		if code != http.StatusBadRequest {
			t.Fatalf("password %q: expected 400, got %d: %s", weak, code, body)
		}
	}
}

func TestAdminCreateRejectsUnknownRoles(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	for _, roles := range []string{`["god_mode"]`, `["member","god_mode"]`, `[""]`} {
		code, body := createUserVia(t, s, adminToken, createUserBody("wangwu", "Interim#2026New", roles))
		if code != http.StatusBadRequest {
			t.Fatalf("roles %s: expected 400, got %d: %s", roles, code, body)
		}
	}
}

func TestAdminCreateAssignsATenantInTheSameCall(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	const tenantID = "11111111-1111-1111-1111-111111111111"
	store.tenants[tenantID] = "计算机学院"
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	body := `{"username":"wangwu","display_name":"王五","password":"Interim#2026New","roles":["member"],` +
		`"tenant_id":"` + tenantID + `"}`
	if code, resp := createUserVia(t, s, adminToken, body); code != http.StatusCreated {
		t.Fatalf("create: %d: %s", code, resp)
	}

	created, err := store.FindAdminUserByUsername(context.Background(), "wangwu")
	if err != nil {
		t.Fatal(err)
	}
	if created.TenantID != tenantID {
		t.Fatalf("tenant = %q, want %q", created.TenantID, tenantID)
	}
}

// A mistyped Tenant must be refused before the account exists, otherwise the
// operator has an orphan to find and delete.
func TestAdminCreateRefusesAnUnknownTenant(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	body := `{"username":"wangwu","display_name":"王五","password":"Interim#2026New","roles":["member"],` +
		`"tenant_id":"99999999-9999-9999-9999-999999999999"}`
	code, resp := createUserVia(t, s, adminToken, body)
	if code != http.StatusBadRequest {
		t.Fatalf("unknown Tenant: expected 400, got %d: %s", code, resp)
	}
	if _, err := store.FindAdminUserByUsername(context.Background(), "wangwu"); err == nil {
		t.Fatal("a refused create must not leave an account behind")
	}
}

// An omitted role list means an ordinary account. Sending `roles` is how the
// operator grants authority; not sending it is how they deliberately do not.
func TestAdminCreateWithoutRolesMakesAMember(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	body := `{"username":"wangwu","display_name":"王五","password":"Interim#2026New"}`
	if code, resp := createUserVia(t, s, adminToken, body); code != http.StatusCreated {
		t.Fatalf("create: %d: %s", code, resp)
	}
	created, err := store.FindAdminUserByUsername(context.Background(), "wangwu")
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Roles) != 1 || created.Roles[0] != RoleMember {
		t.Fatalf("roles = %v, want [member]", created.Roles)
	}
}

// Account creation is the one way to add authority to the system, so it must be
// the same privilege as granting it: super_admin, enforced by the RBAC
// allow-list rather than by the handler.
func TestAdminCreateIsSuperAdminOnly(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	seedMember(t, store, "zhangsan", "Campus#2026Gate")
	memberToken := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")

	code, _ := createUserVia(t, s, memberToken, createUserBody("wangwu", "Interim#2026New", `["super_admin"]`))
	if code != http.StatusForbidden {
		t.Fatalf("a member must not create accounts: expected 403, got %d", code)
	}
	if _, err := store.FindAdminUserByUsername(context.Background(), "wangwu"); err == nil {
		t.Fatal("a refused create must not leave an account behind")
	}
}

func TestAdminCreateSucceedsOnADisabledTargetState(t *testing.T) {
	// A freshly created account is always enabled; this pins that the create
	// path does not inherit the reset path's "disabled accounts are refused"
	// rule, which exists because a reset on a disabled account is misleading.
	s, store := newTestServer(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	adminToken := accessTokenFor(t, s, "campus-admin", "Campus#2026Gate")

	if code, resp := createUserVia(t, s, adminToken,
		createUserBody("wangwu", "Interim#2026New", `["member"]`)); code != http.StatusCreated {
		t.Fatalf("create: %d: %s", code, resp)
	}
	created, err := store.FindAdminUserByUsername(context.Background(), "wangwu")
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != model.AdminStatusEnabled {
		t.Fatalf("a created account must start enabled, got %s", strconv.Itoa(created.Status))
	}
}
