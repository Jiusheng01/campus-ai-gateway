package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseAdminRoles(t *testing.T) {
	roles := parseAdminRoles("gateway_admin, auditor unknown")
	if !roles["gateway_admin"] || !roles["auditor"] || roles["unknown"] {
		t.Fatalf("unexpected roles: %#v", roles)
	}
}

func TestAdminPrincipalPermissions(t *testing.T) {
	cases := []struct {
		role   string
		method string
		path   string
		want   bool
	}{
		{"auditor", http.MethodGet, "/admin/request-logs", true},
		{"auditor", http.MethodPost, "/admin/thinkwatch/sync", false},
		{"gateway_admin", http.MethodPost, "/admin/thinkwatch/sync", true},
		{"gateway_admin", http.MethodPut, "/admin/tenants/x", false},
		{"tenant_admin", http.MethodPut, "/admin/tenants/x", true},
		{"operator", http.MethodPost, "/admin/model-test", true},
		// Per-model quotas are a tenant-level budget, so tenant_admin reaches
		// them. The campus-wide pool is carved out separately in
		// modelQuotaWritable, not here.
		{"tenant_admin", http.MethodGet, "/admin/model-quotas", true},
		{"tenant_admin", http.MethodPut, "/admin/model-quotas", true},
		{"gateway_admin", http.MethodPut, "/admin/model-quotas", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		p := adminPrincipal{User: "u", Roles: map[string]bool{tc.role: true}}
		if got := p.can(req); got != tc.want {
			t.Fatalf("%s %s as %s = %v, want %v", tc.method, tc.path, tc.role, got, tc.want)
		}
	}
}

// TestModelQuotaSystemPoolRequiresSuperAdmin pins the carve-out that lets
// tenant_admin reach /admin/model-quotas without also letting it set a budget
// for the whole university. A request that names no tenant is the campus-wide
// pool, and only super_admin may write it.
func TestModelQuotaSystemPoolRequiresSuperAdmin(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodPut, "/admin/model-quotas", nil)

	for _, tc := range []struct {
		name  string
		roles map[string]bool
		want  bool
	}{
		{"tenant_admin", map[string]bool{"tenant_admin": true}, false},
		{"gateway_admin", map[string]bool{"gateway_admin": true}, false},
		{"super_admin", map[string]bool{"super_admin": true}, true},
	} {
		p := adminPrincipal{User: tc.name, Roles: tc.roles}
		if ok, _ := srv.modelQuotaWritable(req, p, nil); ok != tc.want {
			t.Fatalf("%s writing the campus-wide pool = %v, want %v", tc.name, ok, tc.want)
		}
	}
}
