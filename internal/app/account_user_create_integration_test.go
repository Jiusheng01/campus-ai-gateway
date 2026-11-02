package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// Account creation touches four statements a fake cannot vouch for: the INSERT,
// the reset flag, the Tenant check and the Tenant assignment. Running it against
// a migrated database is the only way to know the SQL agrees with the story.
func TestAdminCreateRunsOnTheRealSchema(t *testing.T) {
	pg := resetIntegrationStore(t)
	s := tenantAssignmentServer(t, pg)
	tenantAssignmentAccount(t, pg, "campus-admin", []string{RoleSuperAdmin})
	tenant, err := pg.CreateTenant(context.Background(), model.Tenant{
		Code: "cs-dept", Name: "计算机学院", Status: 1,
	})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	adminToken := tenantAssignmentToken(t, s, "campus-admin")
	body := `{"username":"wangwu","display_name":"王五","password":"Interim#2026New",` +
		`"roles":["gateway_admin"],"tenant_id":"` + tenant.ID + `"}`
	rec := tenantAssignmentCall(t, s, adminToken, http.MethodPost, "/admin/users", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}

	created, err := pg.FindAdminUserByUsername(context.Background(), "wangwu")
	if err != nil {
		t.Fatalf("re-read the account: %v", err)
	}
	if !created.PasswordResetRequired {
		t.Fatal("password_reset_required was not persisted")
	}
	if created.TenantID != tenant.ID {
		t.Fatalf("tenant_id = %q, want %q", created.TenantID, tenant.ID)
	}
	if len(created.Roles) != 1 || created.Roles[0] != RoleGatewayAdmin {
		t.Fatalf("roles = %v, want [gateway_admin]", created.Roles)
	}

	// The handed-over password signs in but cannot act, and choosing a new one
	// both lifts the fence and is accepted against the real hash.
	token := accessTokenWith(t, s, "wangwu", "Interim#2026New")
	if got := tenantAssignmentCall(t, s, token, http.MethodGet, "/admin/providers", ""); got.Code != http.StatusForbidden {
		t.Fatalf("a handed-over password reached the console: %d", got.Code)
	}
	if got := resetPasswordChangeCall(t, s, token,
		`{"current_password":"Interim#2026New","new_password":"Chosen#2026ByMe"}`); got.Code != http.StatusOK {
		t.Fatalf("choosing a password = %d: %s", got.Code, got.Body.String())
	}
	after, err := pg.FindAdminUserByID(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.PasswordResetRequired {
		t.Fatal("choosing a new password did not clear password_reset_required")
	}
}
