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
)

const consoleTestPassword = "Campus#2026Gate"

// consoleServer wires the real console routes behind an in-memory account
// store. The seam is the HTTP handler, so the middleware, the account lookup
// and the JSON projection all run as they do in production.
func consoleServer(t *testing.T, cfg Config) (*Server, *fakeAdminStore) {
	t.Helper()
	s, store := newTestServer(t, cfg)
	s.routes()
	return s, store
}

func consoleMember(t *testing.T, s *Server, store *fakeAdminStore, username string, assignTenant bool) (model.AdminUser, string) {
	t.Helper()
	hash, err := security.HashPassword(consoleTestPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := store.CreateUser(context.Background(), username, "普通用户"+username, hash, []string{RoleMember})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if assignTenant {
		store.tenants["tenant-campus"] = "校园信息化"
		if _, err := store.AssignAdminUserTenant(context.Background(), user.ID, "tenant-campus"); err != nil {
			t.Fatalf("assign tenant: %v", err)
		}
		user.TenantID = "tenant-campus"
		user.TenantName = "校园信息化"
	}
	tokens, err := s.adminAuth.login(context.Background(), username, consoleTestPassword, "10.0.0.10", "console-test")
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	return user, tokens.AccessToken
}

func consoleCall(t *testing.T, s *Server, token, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local"+path, nil)
	req.Header.Set("X-Request-Id", "console-test")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

type consoleMeBody struct {
	AccountID       string   `json:"account_id"`
	Username        string   `json:"username"`
	DisplayName     string   `json:"display_name"`
	Roles           []string `json:"roles"`
	Status          int      `json:"status"`
	TenantID        string   `json:"tenant_id"`
	TenantName      string   `json:"tenant_name"`
	WorkspaceUsable bool     `json:"workspace_usable"`
}

func decodeConsoleMe(t *testing.T, rec *httptest.ResponseRecorder) consoleMeBody {
	t.Helper()
	var out consoleMeBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode /api/console/me: %v (%s)", err, rec.Body.String())
	}
	return out
}

// A signed-in member with a Tenant sees the account, its roles and the Tenant
// the workspace spends against.
func TestConsoleMeReturnsMemberIdentityAndTenant(t *testing.T) {
	s, store := consoleServer(t, testConfig())
	user, token := consoleMember(t, s, store, "member-one", true)

	rec := consoleCall(t, s, token, "/api/console/me")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/me = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeConsoleMe(t, rec)
	if body.AccountID != user.ID || body.Username != "member-one" {
		t.Fatalf("identity = %+v, want id %s and username member-one", body, user.ID)
	}
	if body.DisplayName != "普通用户member-one" {
		t.Fatalf("display_name = %q", body.DisplayName)
	}
	if len(body.Roles) != 1 || body.Roles[0] != RoleMember {
		t.Fatalf("roles = %v, want [member]", body.Roles)
	}
	if body.Status != model.AdminStatusEnabled {
		t.Fatalf("status = %d, want enabled", body.Status)
	}
	if body.TenantID != "tenant-campus" || body.TenantName != "校园信息化" {
		t.Fatalf("tenant = %q/%q", body.TenantID, body.TenantName)
	}
	if !body.WorkspaceUsable {
		t.Fatal("a member with a Tenant must have a usable workspace")
	}

	// The projection must not grow a session id or any credential material.
	raw := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"session_id", "password_hash", "password", "access_token", "refresh"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("/api/console/me leaked %q: %s", forbidden, rec.Body.String())
		}
	}
}

// An account waiting for a Tenant gets an explicit unassigned state, not a
// fake Tenant and not an error.
func TestConsoleMeReportsUnassignedTenant(t *testing.T) {
	s, store := consoleServer(t, testConfig())
	_, token := consoleMember(t, s, store, "member-waiting", false)

	rec := consoleCall(t, s, token, "/api/console/me")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/me = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeConsoleMe(t, rec)
	if body.TenantID != "" || body.TenantName != "" {
		t.Fatalf("unassigned tenant must be empty, got %q/%q", body.TenantID, body.TenantName)
	}
	if body.WorkspaceUsable {
		t.Fatal("an account without a Tenant must report the workspace as not usable")
	}
}

// The personal console requires an account id. A trusted-header identity has a
// verified username but no account, so it is refused with a distinct code.
func TestConsoleMeRefusesTrustedHeaderIdentity(t *testing.T) {
	cfg := trustedHeaderConfig()
	s, _ := consoleServer(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/api/console/me", nil)
	req.Header.Set("X-Request-Id", "console-test")
	req.Header.Set(cfg.AdminUserHeader, "zhang.san")
	req.Header.Set(cfg.AdminRolesHeader, RoleMember)
	req.Header.Set("X-Campus-Auth-Secret", cfg.AdminProxySecret)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /api/console/me as trusted-header = %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "console_identity_unavailable" {
		t.Fatalf("error code = %q, want console_identity_unavailable", code)
	}
}

// The existing status check is the one that refuses a disabled account: the
// access token may still be valid, but the account behind it may not be.
func TestConsoleMeRefusesDisabledAccount(t *testing.T) {
	s, store := consoleServer(t, testConfig())
	user, token := consoleMember(t, s, store, "member-disabled", true)

	user.Status = model.AdminStatusDisabled
	store.users[user.ID] = user

	rec := consoleCall(t, s, token, "/api/console/me")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled account = %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "account_disabled" {
		t.Fatalf("error code = %q, want account_disabled", code)
	}
}

// Administrators may use the personal console too; roles choose the default
// landing, not the set of workspaces an account may open.
func TestConsoleMeAllowsAdministrator(t *testing.T) {
	s, store := consoleServer(t, testConfig())
	admin := seedAdmin(t, store, "campus-admin", consoleTestPassword)
	tokens, err := s.adminAuth.login(context.Background(), admin.Username, consoleTestPassword, "10.0.0.10", "console-test")
	if err != nil {
		t.Fatalf("login admin: %v", err)
	}

	rec := consoleCall(t, s, tokens.AccessToken, "/api/console/me")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/console/me as admin = %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeConsoleMe(t, rec)
	if len(body.Roles) != 1 || body.Roles[0] != RoleSuperAdmin {
		t.Fatalf("roles = %v, want [super_admin]", body.Roles)
	}
}

// Unauthenticated callers get the existing 401 envelope, not the
// identity-unavailable code: there is no identity to talk about yet.
func TestConsoleMeRequiresAuthentication(t *testing.T) {
	s, _ := consoleServer(t, testConfig())

	rec := consoleCall(t, s, "", "/api/console/me")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "unauthenticated" {
		t.Fatalf("error code = %q, want unauthenticated", code)
	}
}
