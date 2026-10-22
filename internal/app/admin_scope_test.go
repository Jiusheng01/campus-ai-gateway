package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const testScopeTenantID = "11111111-1111-1111-1111-111111111111"

func principalForScope(scope string) adminPrincipal {
	return adminPrincipal{
		Roles:         map[string]bool{RoleTenantAdmin: true},
		ScopeTenantID: scope,
	}
}

// A scoped tenant_admin reaches the tenant tree, the model-pool table, the
// credentials, the read-only model catalog and the request log, and nothing
// else. The endpoint families are only half the check; the handlers narrow
// every tenant and key id to the subtree (see handleTenantByID and friends).
func TestScopedTenantAdminReachesItsManagementEndpoints(t *testing.T) {
	p := principalForScope(testScopeTenantID)
	allowed := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/tenants"},
		{http.MethodPost, "/admin/tenants"},
		{http.MethodGet, "/admin/tenants/" + testScopeTenantID},
		{http.MethodPut, "/admin/tenants/" + testScopeTenantID},
		{http.MethodDelete, "/admin/tenants/" + testScopeTenantID},
		{http.MethodGet, "/admin/tenants/" + testScopeTenantID + "/usage"},
		{http.MethodPost, "/admin/tenants/" + testScopeTenantID + "/allocation"},
		{http.MethodPost, "/admin/tenants/" + testScopeTenantID + "/move"},
		{http.MethodGet, "/admin/tenants/" + testScopeTenantID + "/model-quotas"},
		{http.MethodGet, "/admin/model-quotas"},
		{http.MethodPut, "/admin/model-quotas"},
		{http.MethodDelete, "/admin/model-quotas"},
		// The quota form and the key form both name models, so the collection is
		// readable. Its detail endpoint still is not: it carries routes and
		// channel facts that belong outside a subtree.
		{http.MethodGet, "/admin/models"},
		{http.MethodGet, "/admin/api-keys"},
		{http.MethodPost, "/admin/api-keys"},
		{http.MethodPut, "/admin/api-keys/" + testScopeTenantID},
		{http.MethodDelete, "/admin/api-keys/" + testScopeTenantID},
		{http.MethodGet, "/admin/request-logs"},
		{http.MethodGet, "/admin/request-logs/export"},
		{http.MethodGet, "/admin/request-logs/usage"},
		{http.MethodGet, "/admin/request-logs/" + testScopeTenantID + "/attempts"},
	}
	for _, tc := range allowed {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if !p.can(req) {
			t.Errorf("%s %s must be allowed for a scoped tenant_admin", tc.method, tc.path)
		}
	}

	denied := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/status"},
		{http.MethodGet, "/admin/status/trend"},
		{http.MethodGet, "/admin/providers"},
		{http.MethodPost, "/admin/providers"},
		{http.MethodPost, "/admin/models/publish"},
		{http.MethodGet, "/admin/models/" + testScopeTenantID + "/authorizations"},
		{http.MethodGet, "/admin/admin-audit"},
		{http.MethodGet, "/admin/security-events"},
		{http.MethodPost, "/admin/quota-rebuild"},
		{http.MethodGet, "/admin/users"},
		{http.MethodPatch, "/admin/users/" + testScopeTenantID},
	}
	for _, tc := range denied {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if p.can(req) {
			t.Errorf("%s %s must be refused for a scoped tenant_admin", tc.method, tc.path)
		}
	}
}

// Without a scope the role grants no management endpoint at all. The account is
// still able to read its own identity and rotate its own password, which is the
// self-service grant every signed-in account has.
func TestTenantAdminWithoutScopeGrantsNothingButSelfService(t *testing.T) {
	p := principalForScope("")
	for _, path := range []string{"/admin/tenants", "/admin/model-quotas", "/admin/api-keys", "/admin/request-logs"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if p.can(req) {
			t.Errorf("GET %s must be refused without a scope", path)
		}
	}
	if !p.can(httptest.NewRequest(http.MethodGet, "/admin/whoami", nil)) {
		t.Error("self-service whoami must stay available without a scope")
	}
	if !p.can(httptest.NewRequest(http.MethodPost, "/admin/password", nil)) {
		t.Error("self-service password change must stay available without a scope")
	}
}

func TestValidateAccountScopePairsRoleWithScope(t *testing.T) {
	cases := []struct {
		name   string
		roles  []string
		scope  string
		wantOK bool
	}{
		{"role with scope", []string{RoleTenantAdmin}, testScopeTenantID, true},
		{"role without scope", []string{RoleTenantAdmin}, "", false},
		{"scope without role", []string{RoleMember}, testScopeTenantID, false},
		{"member without scope", []string{RoleMember}, "", true},
		{"global admin without scope", []string{RoleGatewayAdmin}, "", true},
		{"role beside another admin", []string{RoleGatewayAdmin, RoleTenantAdmin}, testScopeTenantID, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAccountScope(tc.roles, tc.scope)
			if tc.wantOK && err != nil {
				t.Fatalf("ValidateAccountScope(%v, %q) = %v, want nil", tc.roles, tc.scope, err)
			}
			if !tc.wantOK && err == nil {
				t.Fatalf("ValidateAccountScope(%v, %q) = nil, want a refusal", tc.roles, tc.scope)
			}
		})
	}
}

// tenant_admin is assignable now that a scope exists, and the global roles are
// unaffected by the change.
func TestTenantAdminIsAssignableThroughTheUserAPI(t *testing.T) {
	if err := ValidateAccountRoles([]string{RoleTenantAdmin}); err != nil {
		t.Fatalf("tenant_admin must be assignable with a scope: %v", err)
	}
	if err := ValidateAccountRoles([]string{"god_mode"}); err == nil {
		t.Fatal("an unknown role must still be refused")
	}
}
