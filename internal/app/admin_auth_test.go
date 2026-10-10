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
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		p := adminPrincipal{User: "u", Roles: map[string]bool{tc.role: true}}
		if got := p.can(req); got != tc.want {
			t.Fatalf("%s %s as %s = %v, want %v", tc.method, tc.path, tc.role, got, tc.want)
		}
	}
}
