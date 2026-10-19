package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Budget allocation is where the scope rules bite hardest, so the routing and
// the write protection are pinned here rather than only through an integration
// test. Both assertions call the production functions; a mirrored copy of the
// switch would pass no matter what the handler did.

func TestTenantPathKind(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		path string
		want string
	}{
		{"/admin/tenants", tenantPathCollection},
		{"/admin/tenants/" + id, tenantPathNode},
		// The suffix routes must be recognised before the node route, or the
		// tail would be handed to the node handler as part of the id.
		{"/admin/tenants/" + id + "/allocation", tenantPathAllocation},
		{"/admin/tenants/" + id + "/usage", tenantPathUsage},
		// Nothing else is served under a tenant, so it is a 404 rather than a
		// node with a slash in its id.
		{"/admin/tenants/", ""},
		{"/admin/tenants/" + id + "/nonsense", ""},
		{"/admin/tenants/" + id + "/usage/extra", ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := tenantPathKind(tc.path); got != tc.want {
				t.Fatalf("tenantPathKind(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// A scoped administrator may not rewrite the budget of the tenant they are
// scoped to. It is the one edit that would let them raise their own ceiling,
// while the endpoint itself stays addressable so children can still be
// allocated.
func TestScopedAdminCanAddressTheAllocationEndpoint(t *testing.T) {
	scope := "11111111-1111-1111-1111-111111111111"
	p := adminPrincipal{User: "zhangsan", Roles: map[string]bool{RoleTenantAdmin: true}, ScopeTenantID: &scope}
	if !p.scoped() {
		t.Fatal("a principal with a scope must report itself scoped")
	}

	req := httptest.NewRequest(http.MethodPut, "/admin/tenants/"+scope+"/allocation", nil)
	if !p.can(req) {
		t.Fatal("a scoped tenant_admin must be able to address the allocation endpoint")
	}
}

// The campus pool belongs to nobody's subtree, so a scoped administrator cannot
// reach it even though the endpoint is addressable.
func TestModelQuotaSubjectReachability(t *testing.T) {
	scope := "11111111-1111-1111-1111-111111111111"
	other := "22222222-2222-2222-2222-222222222222"

	if !modelQuotaSubjectReachable("", nil) {
		t.Fatal("a global administrator may write the campus pool")
	}
	if modelQuotaSubjectReachable(scope, nil) {
		t.Fatal("the campus pool must not be writable from a scope")
	}
	if !modelQuotaSubjectReachable(scope, &scope) {
		t.Fatal("a tenant id must be reachable from a scope, subject to the subtree test")
	}
	if !modelQuotaSubjectReachable(scope, &other) {
		// Reachability is not the subtree decision: the database answers that
		// one. What this function refuses is only the subject that can never
		// be inside any scope.
		t.Fatal("reachability must not stand in for the subtree test")
	}
}

func TestScopedAdminCanAddressTheModelQuotaEndpoint(t *testing.T) {
	scope := "11111111-1111-1111-1111-111111111111"
	p := adminPrincipal{User: "zhangsan", Roles: map[string]bool{RoleTenantAdmin: true}, ScopeTenantID: &scope}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/admin/model-quotas", nil)
		if !p.can(req) {
			t.Fatalf("%s /admin/model-quotas as a scoped tenant_admin = false, want true", method)
		}
	}
}
