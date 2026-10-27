package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/google/uuid"
)

// These tests run at the HTTP seam against a migrated database, like the usage
// integration tests: the rule under test is a property of the transaction (the
// key count and the write cannot disagree), which an in-memory fake can only
// confirm about itself.

const tenantAssignmentAdminPassword = "Campus#2026Gate"

// tenantAssignmentServer wires the real storage layer into the admin auth
// service, so login, RBAC and the handlers all run exactly as they do in
// production. The login limiter is the package fake because throttling is not
// what this suite is about.
func tenantAssignmentServer(t *testing.T, pg *storage.Postgres) *Server {
	t.Helper()
	cfg := testConfig()
	s := &Server{cfg: cfg, pg: pg}
	s.adminAuth = newAdminAuthService(pg, newFakeLimiter(8), []byte("tenant-assignment-test-secret"), cfg)
	return s
}

// tenantAssignmentClear removes the rows these tests seed. Sessions go first
// because they reference the accounts; keys go before tenants for the same
// reason.
func tenantAssignmentClear(t *testing.T, pg *storage.Postgres) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM gateway_admin_session`,
		`DELETE FROM gateway_request_log`,
		`DELETE FROM gateway_api_key`,
		`DELETE FROM gateway_admin_user`,
		`DELETE FROM gateway_tenant`,
	} {
		if _, err := pg.Pool.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("clear %q: %v", stmt, err)
		}
	}
}

func tenantAssignmentAccount(t *testing.T, pg *storage.Postgres, username string, roles []string) model.AdminUser {
	t.Helper()
	hash, err := security.HashPassword(tenantAssignmentAdminPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u, err := pg.CreateUser(context.Background(), username, username, hash, roles, false)
	if err != nil {
		t.Fatalf("create account %s: %v", username, err)
	}
	return u
}

func tenantAssignmentToken(t *testing.T, s *Server, username string) string {
	t.Helper()
	tokens, err := s.adminAuth.login(context.Background(), username, tenantAssignmentAdminPassword, "10.0.0.10", "test-agent")
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	return tokens.AccessToken
}

func tenantAssignmentCall(t *testing.T, s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Request-Id", "tenant-assignment-test")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	// The production mux splits the collection from a single account; the test
	// mirrors that split so the list is read through its own handler.
	handler := http.HandlerFunc(s.handleAdminUserByID)
	if path == "/admin/users" {
		handler = s.handleAdminUsers
	}
	rec := httptest.NewRecorder()
	s.adminMiddleware(handler).ServeHTTP(rec, req)
	return rec
}

type tenantAssignmentView struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
}

func decodeTenantAssignmentView(t *testing.T, rec *httptest.ResponseRecorder) tenantAssignmentView {
	t.Helper()
	var envelope struct {
		Data tenantAssignmentView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return envelope.Data
}

// A list response is the reload the console performs after a save, so the
// assignment test asserts through it as well as through the PUT response.
func tenantAssignmentListed(t *testing.T, s *Server, token, accountID string) tenantAssignmentView {
	t.Helper()
	rec := tenantAssignmentCall(t, s, token, http.MethodGet, "/admin/users", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/users = %d: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data []tenantAssignmentView `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode user list: %v", err)
	}
	for _, u := range envelope.Data {
		if u.ID == accountID {
			return u
		}
	}
	t.Fatalf("account %s missing from the user list", accountID)
	return tenantAssignmentView{}
}

func tenantAssignmentJSON(tenantID string) string {
	return `{"tenant_id":"` + tenantID + `"}`
}

func uniqueAccountName(prefix string) string {
	return prefix + "-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
}

// Assign, reload, reassign: the three observable steps the console performs.
func TestAdminAssignsAndReassignsATenant(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := tenantAssignmentServer(t, pg)

	first := usageSeedTenant(t, pg, "assign-first", 0, 0, 0)
	second := usageSeedTenant(t, pg, "assign-second", 0, 0, 0)

	admin := tenantAssignmentAccount(t, pg, uniqueAccountName("assign-admin"), []string{RoleSuperAdmin})
	member := tenantAssignmentAccount(t, pg, uniqueAccountName("assign-member"), []string{RoleMember})
	token := tenantAssignmentToken(t, s, admin.Username)

	before := tenantAssignmentListed(t, s, token, member.ID)
	if before.TenantID != "" || before.TenantName != "" {
		t.Fatalf("new account starts assigned to %q/%q, want none", before.TenantID, before.TenantName)
	}

	rec := tenantAssignmentCall(t, s, token, http.MethodPut, "/admin/users/"+member.ID+"/tenant", tenantAssignmentJSON(first.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("assign = %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeTenantAssignmentView(t, rec); got.TenantID != first.ID || got.TenantName != first.Name {
		t.Fatalf("assign returned %q/%q, want %q/%q", got.TenantID, got.TenantName, first.ID, first.Name)
	}

	afterAssign := tenantAssignmentListed(t, s, token, member.ID)
	if afterAssign.TenantID != first.ID || afterAssign.TenantName != first.Name {
		t.Fatalf("list after assign shows %q/%q, want %q/%q", afterAssign.TenantID, afterAssign.TenantName, first.ID, first.Name)
	}

	rec = tenantAssignmentCall(t, s, token, http.MethodPut, "/admin/users/"+member.ID+"/tenant", tenantAssignmentJSON(second.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("reassign = %d: %s", rec.Code, rec.Body.String())
	}
	afterReassign := tenantAssignmentListed(t, s, token, member.ID)
	if afterReassign.TenantID != second.ID || afterReassign.TenantName != second.Name {
		t.Fatalf("list after reassign shows %q/%q, want %q/%q", afterReassign.TenantID, afterReassign.TenantName, second.ID, second.Name)
	}
}

// A key must never point at a Tenant its owner has left, so the assignment is
// refused while any key is owned and the refusal names how many.
func TestTenantAssignmentRefusedWhileTheAccountOwnsKeys(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := tenantAssignmentServer(t, pg)

	target := usageSeedTenant(t, pg, "assign-target", 0, 0, 0)
	admin := tenantAssignmentAccount(t, pg, uniqueAccountName("keys-admin"), []string{RoleSuperAdmin})
	member := tenantAssignmentAccount(t, pg, uniqueAccountName("keys-member"), []string{RoleMember})
	token := tenantAssignmentToken(t, s, admin.Username)

	key := usageSeedKey(t, pg, target, "owned-key")
	if _, err := pg.Pool.Exec(context.Background(),
		`UPDATE gateway_api_key SET owner_user_id=$1::uuid WHERE id=$2::uuid`, member.ID, key); err != nil {
		t.Fatalf("assign key owner: %v", err)
	}

	rec := tenantAssignmentCall(t, s, token, http.MethodPut, "/admin/users/"+member.ID+"/tenant", tenantAssignmentJSON(target.ID))
	if rec.Code != http.StatusConflict {
		t.Fatalf("assign while owning a key = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Code          string `json:"code"`
			OwnedKeyCount int64  `json:"owned_key_count"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if envelope.Error.Code != "user_owns_keys" {
		t.Fatalf("refusal code = %q, want user_owns_keys", envelope.Error.Code)
	}
	if envelope.Error.OwnedKeyCount != 1 {
		t.Fatalf("owned_key_count = %d, want 1", envelope.Error.OwnedKeyCount)
	}

	// The refusal must be complete: the account is still unassigned.
	var tenantID *string
	if err := pg.Pool.QueryRow(context.Background(),
		`SELECT tenant_id::text FROM gateway_admin_user WHERE id=$1::uuid`, member.ID).Scan(&tenantID); err != nil {
		t.Fatalf("read membership after refusal: %v", err)
	}
	if tenantID != nil {
		t.Fatalf("membership moved despite the refusal: %q", *tenantID)
	}
}

// An assignment naming a Tenant that does not exist is a bad request, not a
// foreign-key failure.
func TestTenantAssignmentRejectsUnknownTenant(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := tenantAssignmentServer(t, pg)

	admin := tenantAssignmentAccount(t, pg, uniqueAccountName("unknown-admin"), []string{RoleSuperAdmin})
	member := tenantAssignmentAccount(t, pg, uniqueAccountName("unknown-member"), []string{RoleMember})
	token := tenantAssignmentToken(t, s, admin.Username)

	rec := tenantAssignmentCall(t, s, token, http.MethodPut, "/admin/users/"+member.ID+"/tenant", tenantAssignmentJSON(uuid.NewString()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("assign unknown tenant = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if code := errorCodeFromBody(t, rec); code != "tenant_not_found" {
		t.Fatalf("error code = %q, want tenant_not_found", code)
	}
}

// The user endpoints already refuse a member; adding the Tenant field must not
// widen RBAC.
func TestMemberCannotReachTenantAssignment(t *testing.T) {
	pg := usagePostgres(t)
	tenantAssignmentClear(t, pg)
	s := tenantAssignmentServer(t, pg)

	tenant := usageSeedTenant(t, pg, "member-denied", 0, 0, 0)
	member := tenantAssignmentAccount(t, pg, uniqueAccountName("denied-member"), []string{RoleMember})
	token := tenantAssignmentToken(t, s, member.Username)

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/admin/users", ""},
		{http.MethodPatch, "/admin/users/" + member.ID, `{"display_name":"nope"}`},
		{http.MethodPut, "/admin/users/" + member.ID + "/tenant", tenantAssignmentJSON(tenant.ID)},
	} {
		rec := tenantAssignmentCall(t, s, token, tc.method, tc.path, tc.body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s as member = %d, want 403: %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

func errorCodeFromBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return envelope.Error.Code
}
