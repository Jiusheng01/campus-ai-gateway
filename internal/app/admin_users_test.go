package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
)

// --- create -----------------------------------------------------------------

func TestCreateAdminUserSucceeds(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "root", "Root#Password1")

	user, err := svc.createAdminUser(context.Background(), "ZhangSan", "张三", "ZhangSan#2026", []string{"gateway_admin"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if user.ID == "" || user.Username != "ZhangSan" {
		t.Fatalf("unexpected user: %+v", user)
	}
	if user.PasswordHash != "" {
		t.Fatal("the password hash must never be handed back to the caller")
	}
	if !reflect.DeepEqual(user.Roles, []string{"gateway_admin"}) {
		t.Fatalf("unexpected roles: %v", user.Roles)
	}
	if user.Status != model.AdminStatusEnabled {
		t.Fatalf("a new account must start enabled, got status %d", user.Status)
	}

	// The stored value has to be a real Argon2id hash of the password, not the
	// plaintext and not some placeholder.
	stored := store.users[user.ID]
	if !strings.HasPrefix(stored.PasswordHash, "$argon2id$") {
		t.Fatalf("stored hash is not argon2id: %q", stored.PasswordHash)
	}
	valid, _, err := security.VerifyPassword(stored.PasswordHash, "ZhangSan#2026")
	if err != nil || !valid {
		t.Fatalf("stored hash does not verify (valid=%v err=%v)", valid, err)
	}
}

func TestCreateAdminUserRejectsDuplicateUsername(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "root", "Root#Password1")

	if _, err := svc.createAdminUser(context.Background(), "Ops", "运维", "Ops#Password2026", []string{"auditor"}, nil); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// The unique index is on lower(username), so a different casing collides.
	_, err := svc.createAdminUser(context.Background(), "OPS", "另一个运维", "Ops#Password2026", []string{"auditor"}, nil)
	if !errors.Is(err, ErrAdminUsernameTaken) {
		t.Fatalf("expected ErrAdminUsernameTaken, got %v", err)
	}
}

func TestCreateAdminUserRejectsWeakPassword(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "root", "Root#Password1")

	for _, tc := range []struct{ name, password string }{
		{"too short", "Ab#1"},
		{"single character class", "aaaaaaaaaaaaaa"},
		{"same as username", "OpsAccount1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.createAdminUser(context.Background(), "OpsAccount1", "运维", tc.password, []string{"auditor"}, nil)
			if !errors.Is(err, ErrAdminPasswordWeak) {
				t.Fatalf("expected ErrAdminPasswordWeak, got %v", err)
			}
		})
	}
}

func TestCreateAdminUserRejectsMissingOrUnknownRoles(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "root", "Root#Password1")

	for _, tc := range []struct {
		name  string
		roles []string
	}{
		{"no roles", nil},
		{"empty list", []string{}},
		{"only unknown", []string{"root", "admin", ""}},
		{"whitespace only", []string{"   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A typo must be rejected rather than silently downgraded to
			// auditor, otherwise the operator believes they granted something
			// they did not.
			_, err := svc.createAdminUser(context.Background(), "Ops", "运维", "Ops#Password2026", tc.roles, nil)
			if !errors.Is(err, ErrAdminRolesInvalid) {
				t.Fatalf("expected ErrAdminRolesInvalid, got %v", err)
			}
		})
	}
}

func TestCreateAdminUserNormalizesRoles(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "root", "Root#Password1")

	// Duplicates collapse and the order is fixed regardless of input order.
	user, err := svc.createAdminUser(context.Background(), "Ops", "运维", "Ops#Password2026",
		[]string{"auditor", "gateway_admin", "auditor", " super_admin "}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	want := []string{"super_admin", "gateway_admin", "auditor"}
	if !reflect.DeepEqual(user.Roles, want) {
		t.Fatalf("expected %v, got %v", want, user.Roles)
	}
}

func TestCreateAdminUserRejectsOverlongUsername(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "root", "Root#Password1")

	_, err := svc.createAdminUser(context.Background(), strings.Repeat("a", 65), "太长", "Ops#Password2026", []string{"auditor"}, nil)
	if !errors.Is(err, ErrAdminBadRequest) {
		t.Fatalf("expected ErrAdminBadRequest, got %v", err)
	}
}

// --- list -------------------------------------------------------------------

func TestListAdminUsersStripsPasswordHash(t *testing.T) {
	svc, store, _ := newTestService(t)
	seedAdmin(t, store, "root", "Root#Password1")
	if _, err := svc.createAdminUser(context.Background(), "Ops", "运维", "Ops#Password2026", []string{"auditor"}, nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	users, err := svc.listAdminUsers(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(users))
	}
	for _, u := range users {
		if u.PasswordHash != "" {
			t.Fatalf("account %s leaked its password hash", u.Username)
		}
	}
}

// --- update roles -----------------------------------------------------------

func TestUpdateAdminUserRolesSucceeds(t *testing.T) {
	svc, store, _ := newTestService(t)
	root := seedAdmin(t, store, "root", "Root#Password1")
	target, err := svc.createAdminUser(context.Background(), "Ops", "运维", "Ops#Password2026", []string{"auditor"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := svc.updateAdminUserRoles(context.Background(), root.ID, target.ID, []string{"tenant_admin", "auditor"})
	if err != nil {
		t.Fatalf("update roles: %v", err)
	}
	want := []string{"tenant_admin", "auditor"}
	if !reflect.DeepEqual(updated.Roles, want) {
		t.Fatalf("expected %v, got %v", want, updated.Roles)
	}
	if updated.PasswordHash != "" {
		t.Fatal("the password hash must never be handed back to the caller")
	}
	// A role change must not invalidate the target's credentials: roles are
	// read per request, so the change is already live.
	if store.users[target.ID].AuthVersion != target.AuthVersion {
		t.Fatal("changing roles must not bump auth_version")
	}
}

func TestUpdateAdminUserRolesRejectsSelfEdit(t *testing.T) {
	svc, store, _ := newTestService(t)
	root := seedAdmin(t, store, "root", "Root#Password1")

	_, err := svc.updateAdminUserRoles(context.Background(), root.ID, root.ID, []string{"super_admin", "gateway_admin"})
	if !errors.Is(err, ErrAdminSelfModification) {
		t.Fatalf("expected ErrAdminSelfModification, got %v", err)
	}
}

func TestUpdateAdminUserRolesProtectsLastSuperAdmin(t *testing.T) {
	svc, store, _ := newTestService(t)
	root := seedAdmin(t, store, "root", "Root#Password1")
	other, err := svc.createAdminUser(context.Background(), "Ops", "运维", "Ops#Password2026", []string{"gateway_admin"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Demoting the only super administrator would make the system
	// permanently unmanageable.
	_, err = svc.updateAdminUserRoles(context.Background(), other.ID, root.ID, []string{"auditor"})
	if !errors.Is(err, ErrAdminLastSuperAdmin) {
		t.Fatalf("expected ErrAdminLastSuperAdmin, got %v", err)
	}
	if !containsSuperAdmin(store.users[root.ID].Roles) {
		t.Fatal("the guard must leave the account untouched")
	}
}

func TestUpdateAdminUserRolesAllowsDemotionWithASecondSuperAdmin(t *testing.T) {
	svc, store, _ := newTestService(t)
	root := seedAdmin(t, store, "root", "Root#Password1")
	second, err := svc.createAdminUser(context.Background(), "Root2", "二号超管", "Second#Password2026", []string{"super_admin"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.updateAdminUserRoles(context.Background(), second.ID, root.ID, []string{"auditor"}); err != nil {
		t.Fatalf("demote with a second super admin present: %v", err)
	}
	if containsSuperAdmin(store.users[root.ID].Roles) {
		t.Fatal("the demotion should have been applied")
	}
}

func TestUpdateAdminUserRolesRejectsUnknownTarget(t *testing.T) {
	svc, store, _ := newTestService(t)
	root := seedAdmin(t, store, "root", "Root#Password1")

	_, err := svc.updateAdminUserRoles(context.Background(), root.ID, "no-such-id", []string{"auditor"})
	if !errors.Is(err, ErrAdminUserNotFound) {
		t.Fatalf("expected ErrAdminUserNotFound, got %v", err)
	}
}

// --- enable / disable -------------------------------------------------------

func TestSetAdminUserStatusRejectsSelf(t *testing.T) {
	svc, store, _ := newTestService(t)
	root := seedAdmin(t, store, "root", "Root#Password1")

	_, err := svc.setAdminUserStatus(context.Background(), root.ID, root.ID, model.AdminStatusDisabled)
	if !errors.Is(err, ErrAdminSelfModification) {
		t.Fatalf("expected ErrAdminSelfModification, got %v", err)
	}
}

func TestSetAdminUserStatusDisablesAndRevokesSessions(t *testing.T) {
	svc, store, _ := newTestService(t)
	root := seedAdmin(t, store, "root", "Root#Password1")
	target, err := svc.createAdminUser(context.Background(), "Ops", "运维", "Ops#Password2026", []string{"gateway_admin"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.login(context.Background(), "Ops", "Ops#Password2026", "10.0.0.5", "test-agent"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if len(store.sessions) != 1 {
		t.Fatalf("expected one session, got %d", len(store.sessions))
	}

	updated, err := svc.setAdminUserStatus(context.Background(), root.ID, target.ID, model.AdminStatusDisabled)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if updated.Status != model.AdminStatusDisabled {
		t.Fatalf("expected disabled, got %d", updated.Status)
	}

	// The account must be signed out immediately, not on its next request.
	for sid, s := range store.sessions {
		if s.RevokedAt == nil {
			t.Fatalf("session %s was left active", sid)
		}
		if s.RevokeReason != revokeReasonDisabled {
			t.Fatalf("expected reason %q, got %q", revokeReasonDisabled, s.RevokeReason)
		}
	}

	// And it must no longer be able to sign in.
	if _, err := svc.login(context.Background(), "Ops", "Ops#Password2026", "10.0.0.5", "test-agent"); !errors.Is(err, ErrAdminUserDisabled) {
		t.Fatalf("expected ErrAdminUserDisabled on login, got %v", err)
	}
}

func TestSetAdminUserStatusProtectsLastSuperAdmin(t *testing.T) {
	svc, store, _ := newTestService(t)
	root := seedAdmin(t, store, "root", "Root#Password1")
	other, err := svc.createAdminUser(context.Background(), "Ops", "运维", "Ops#Password2026", []string{"gateway_admin"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.setAdminUserStatus(context.Background(), other.ID, root.ID, model.AdminStatusDisabled)
	if !errors.Is(err, ErrAdminLastSuperAdmin) {
		t.Fatalf("expected ErrAdminLastSuperAdmin, got %v", err)
	}
	if store.users[root.ID].Status != model.AdminStatusEnabled {
		t.Fatal("the guard must leave the account enabled")
	}
}

func TestSetAdminUserStatusRejectsUnknownStatus(t *testing.T) {
	svc, store, _ := newTestService(t)
	root := seedAdmin(t, store, "root", "Root#Password1")
	target, err := svc.createAdminUser(context.Background(), "Ops", "运维", "Ops#Password2026", []string{"auditor"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, status := range []int{0, 3, -1, 99} {
		if _, err := svc.setAdminUserStatus(context.Background(), root.ID, target.ID, status); !errors.Is(err, ErrAdminBadRequest) {
			t.Fatalf("status %d: expected ErrAdminBadRequest, got %v", status, err)
		}
	}
}

func TestSetAdminUserStatusCanReEnable(t *testing.T) {
	svc, store, _ := newTestService(t)
	root := seedAdmin(t, store, "root", "Root#Password1")
	target, err := svc.createAdminUser(context.Background(), "Ops", "运维", "Ops#Password2026", []string{"auditor"}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.setAdminUserStatus(context.Background(), root.ID, target.ID, model.AdminStatusDisabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	reenabled, err := svc.setAdminUserStatus(context.Background(), root.ID, target.ID, model.AdminStatusEnabled)
	if err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if reenabled.Status != model.AdminStatusEnabled {
		t.Fatalf("expected enabled, got %d", reenabled.Status)
	}
}

// --- error mapping ----------------------------------------------------------

func TestAdminUserErrorStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrAdminUsernameTaken, http.StatusConflict, "username_taken"},
		{ErrAdminLastSuperAdmin, http.StatusConflict, "last_super_admin"},
		{ErrAdminSelfModification, http.StatusForbidden, "self_modification_forbidden"},
		{ErrAdminRolesInvalid, http.StatusBadRequest, "invalid_roles"},
	} {
		status, code := adminErrorStatus(tc.err)
		if status != tc.status || code != tc.code {
			t.Fatalf("%v: expected %d/%s, got %d/%s", tc.err, tc.status, tc.code, status, code)
		}
	}
}

// --- HTTP -------------------------------------------------------------------

// seedSuperAdmin creates the initial super administrator and signs in, returning
// the account and a usable access token.
func seedSuperAdmin(t *testing.T, s *Server, store *fakeAdminStore) (model.AdminUser, string) {
	t.Helper()
	hash, err := security.HashPassword("SuperAdmin#2026")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := store.CreateFirstAdminUser(t.Context(), "root", "Root", hash, []string{"super_admin"})
	if err != nil {
		t.Fatalf("seed super admin: %v", err)
	}
	rec := postJSON(t, http.HandlerFunc(s.handleAdminLogin), http.MethodPost, "/admin/login",
		`{"username":"root","password":"SuperAdmin#2026"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("super admin login failed: %d %s", rec.Code, rec.Body.String())
	}
	token, _ := decodeBody(t, rec)["access_token"].(string)
	return u, token
}

func TestAdminUsersEndpointRejectsNonSuperAdmin(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	token := seedAuditor(t, s, store)

	handler := s.adminMiddleware(http.HandlerFunc(s.handleAdminUsers))

	// The auditor role grants no account-administration path, so the request
	// must be stopped by RBAC before the handler runs.
	rec := postJSON(t, handler, http.MethodGet, "/admin/admin-users", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "admin_forbidden" {
		t.Fatalf("expected admin_forbidden, got %q", code)
	}
}

func TestAdminUsersEndpointCreateAndList(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	_, token := seedSuperAdmin(t, s, store)
	auth := map[string]string{"Authorization": "Bearer " + token}

	handler := s.adminMiddleware(http.HandlerFunc(s.handleAdminUsers))

	rec := postJSON(t, handler, http.MethodPost, "/admin/admin-users",
		`{"username":"Ops","display_name":"运维","password":"Ops#Password2026","roles":["gateway_admin"]}`, auth)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["username"] != "Ops" {
		t.Fatalf("unexpected body: %v", body)
	}
	if _, leaked := body["password_hash"]; leaked {
		t.Fatal("the response must not contain password_hash")
	}

	rec = postJSON(t, handler, http.MethodGet, "/admin/admin-users", "", auth)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	if strings.Contains(raw, "password_hash") || strings.Contains(raw, "$argon2id$") {
		t.Fatalf("the list response leaked a password hash: %s", raw)
	}
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(payload.Data))
	}
}

func TestAdminUsersEndpointPatchRoles(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	_, token := seedSuperAdmin(t, s, store)
	auth := map[string]string{"Authorization": "Bearer " + token}

	created := postJSON(t, s.adminMiddleware(http.HandlerFunc(s.handleAdminUsers)), http.MethodPost, "/admin/admin-users",
		`{"username":"Ops","display_name":"运维","password":"Ops#Password2026","roles":["auditor"]}`, auth)
	if created.Code != http.StatusCreated {
		t.Fatalf("create failed: %d %s", created.Code, created.Body.String())
	}
	id, _ := decodeBody(t, created)["id"].(string)

	handler := s.adminMiddleware(http.HandlerFunc(s.handleAdminUserByID))
	rec := postJSON(t, handler, http.MethodPatch, "/admin/admin-users/"+id, `{"roles":["tenant_admin"]}`, auth)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	roles, _ := decodeBody(t, rec)["roles"].([]any)
	if len(roles) != 1 || roles[0] != "tenant_admin" {
		t.Fatalf("unexpected roles: %v", roles)
	}
}

func TestAdminUsersEndpointPatchStatusDisablesAccount(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	_, token := seedSuperAdmin(t, s, store)
	auth := map[string]string{"Authorization": "Bearer " + token}

	created := postJSON(t, s.adminMiddleware(http.HandlerFunc(s.handleAdminUsers)), http.MethodPost, "/admin/admin-users",
		`{"username":"Ops","display_name":"运维","password":"Ops#Password2026","roles":["auditor"]}`, auth)
	id, _ := decodeBody(t, created)["id"].(string)

	handler := s.adminMiddleware(http.HandlerFunc(s.handleAdminUserByID))
	rec := postJSON(t, handler, http.MethodPatch, "/admin/admin-users/"+id, `{"status":2}`, auth)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if status, _ := decodeBody(t, rec)["status"].(float64); int(status) != model.AdminStatusDisabled {
		t.Fatalf("expected disabled, got %v", status)
	}
}

func TestAdminUsersEndpointPatchRejectsEmptyBody(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	_, token := seedSuperAdmin(t, s, store)
	auth := map[string]string{"Authorization": "Bearer " + token}

	created := postJSON(t, s.adminMiddleware(http.HandlerFunc(s.handleAdminUsers)), http.MethodPost, "/admin/admin-users",
		`{"username":"Ops","display_name":"运维","password":"Ops#Password2026","roles":["auditor"]}`, auth)
	id, _ := decodeBody(t, created)["id"].(string)

	handler := s.adminMiddleware(http.HandlerFunc(s.handleAdminUserByID))
	rec := postJSON(t, handler, http.MethodPatch, "/admin/admin-users/"+id, `{}`, auth)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminUsersEndpointRejectsSelfDemotion(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	root, token := seedSuperAdmin(t, s, store)
	auth := map[string]string{"Authorization": "Bearer " + token}

	handler := s.adminMiddleware(http.HandlerFunc(s.handleAdminUserByID))
	rec := postJSON(t, handler, http.MethodPatch, "/admin/admin-users/"+root.ID, `{"roles":["auditor"]}`, auth)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "self_modification_forbidden" {
		t.Fatalf("expected self_modification_forbidden, got %q", code)
	}
}

func TestAdminUsersEndpointRejectsDuplicateUsername(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	_, token := seedSuperAdmin(t, s, store)
	auth := map[string]string{"Authorization": "Bearer " + token}

	handler := s.adminMiddleware(http.HandlerFunc(s.handleAdminUsers))
	body := `{"username":"Ops","display_name":"运维","password":"Ops#Password2026","roles":["auditor"]}`
	if rec := postJSON(t, handler, http.MethodPost, "/admin/admin-users", body, auth); rec.Code != http.StatusCreated {
		t.Fatalf("first create failed: %d %s", rec.Code, rec.Body.String())
	}
	rec := postJSON(t, handler, http.MethodPost, "/admin/admin-users", body, auth)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "username_taken" {
		t.Fatalf("expected username_taken, got %q", code)
	}
}

func TestAdminUsersEndpointRejectsWrongMethod(t *testing.T) {
	s, store := newTestServer(t, testConfig())
	_, token := seedSuperAdmin(t, s, store)
	auth := map[string]string{"Authorization": "Bearer " + token}

	handler := s.adminMiddleware(http.HandlerFunc(s.handleAdminUsers))
	rec := postJSON(t, handler, http.MethodDelete, "/admin/admin-users", "", auth)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- RBAC table -------------------------------------------------------------

func TestAccountAdministrationIsSuperAdminOnly(t *testing.T) {
	paths := []struct{ method, path string }{
		{http.MethodGet, "/admin/admin-users"},
		{http.MethodPost, "/admin/admin-users"},
		{http.MethodPatch, "/admin/admin-users/abc"},
	}

	// Every other role must be refused, and super_admin must be allowed.
	for _, role := range []string{"gateway_admin", "tenant_admin", "auditor", "operator"} {
		p := adminPrincipal{Roles: map[string]bool{role: true}}
		for _, tc := range paths {
			req := httptest.NewRequest(tc.method, "http://gateway.local"+tc.path, nil)
			if p.can(req) {
				t.Fatalf("role %s must not reach %s %s", role, tc.method, tc.path)
			}
		}
	}

	root := adminPrincipal{Roles: map[string]bool{"super_admin": true}}
	for _, tc := range paths {
		req := httptest.NewRequest(tc.method, "http://gateway.local"+tc.path, nil)
		if !root.can(req) {
			t.Fatalf("super_admin must reach %s %s", tc.method, tc.path)
		}
	}
}
