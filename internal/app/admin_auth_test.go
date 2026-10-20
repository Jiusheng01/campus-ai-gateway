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
	// A management role present means "member" is not part of the answer: the
	// account is an administrator, and saying both would leave two readings.
	if roles[RoleMember] {
		t.Fatalf("member must be dropped once a management role is present: %#v", roles)
	}
}

// P1-01. A proxy group that maps to no global privilege is not a role. Keeping
// it would leave the principal holding a label that reads like authority while
// granting none, and the whoami response would disagree with what the account
// can actually do.
func TestParseAdminRolesDropsRolelessGroups(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", []string{RoleMember}},
		{"blank", "   ", []string{RoleMember}},
		{"separators only", " , ; , ", []string{RoleMember}},
		{"unknown only", "god_mode", []string{RoleMember}},
		{"several unknown", "god_mode,root,;", []string{RoleMember}},
		// The global form of tenant_admin was removed in Phase 1, so a proxy
		// still sending it must not hand the account a management identity.
		{"legacy tenant_admin", RoleTenantAdmin, []string{RoleMember}},
		{"legacy tenant_admin beside member", "member," + RoleTenantAdmin, []string{RoleMember}},
		{"known role survives", RoleAuditor, []string{RoleAuditor}},
		{"known plus unknown", RoleGatewayAdmin + ",unknown", []string{RoleGatewayAdmin}},
		{"member beside management", "member," + RoleAuditor, []string{RoleAuditor}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAdminRoles(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("parseAdminRoles(%q) = %#v, want %v", tc.raw, got, tc.want)
			}
			for _, role := range tc.want {
				if !got[role] {
					t.Fatalf("parseAdminRoles(%q) = %#v, missing %q", tc.raw, got, role)
				}
			}
		})
	}
}

func TestAdminPrincipalPermissions(t *testing.T) {
	scope := "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		role   string
		scope  *string
		method string
		path   string
		want   bool
	}{
		{"auditor", nil, http.MethodGet, "/admin/request-logs", true},
		{"auditor", nil, http.MethodPost, "/admin/models", false},
		// The summary and the trend are reads of telemetry the same roles
		// already see, so they follow the pages they feed rather than opening
		// a new grant.
		{"auditor", nil, http.MethodGet, "/admin/request-logs/summary", true},
		{"auditor", nil, http.MethodGet, "/admin/status/trend", true},
		{"operator", nil, http.MethodGet, "/admin/status/trend", true},
		{"gateway_admin", nil, http.MethodGet, "/admin/status/trend", true},
		// Reading a chart is not revoking a credential.
		{"auditor", nil, http.MethodDelete, "/admin/api-keys/x", false},
		{"operator", nil, http.MethodDelete, "/admin/api-keys/x", false},
		{"gateway_admin", nil, http.MethodPost, "/admin/providers", true},
		{"gateway_admin", nil, http.MethodPut, "/admin/tenants/x", false},
		// The setup checklist counts deployment-wide rows, so it belongs to the
		// roles that configure the deployment, not to those that read telemetry.
		{"gateway_admin", nil, http.MethodGet, "/admin/onboarding", true},
		{"auditor", nil, http.MethodGet, "/admin/onboarding", false},
		{"operator", nil, http.MethodGet, "/admin/onboarding", false},
		// A tenant_admin without a scope is the withdrawn global grant and
		// still reaches nothing. The scope is what turns the role on.
		{"tenant_admin", nil, http.MethodPut, "/admin/tenants/x", false},
		{"tenant_admin", nil, http.MethodGet, "/admin/tenants", false},
		{"tenant_admin", nil, http.MethodPost, "/admin/api-keys", false},
		{"tenant_admin", nil, http.MethodGet, "/admin/status", false},
		{"tenant_admin", nil, http.MethodGet, "/admin/status/trend", false},
		// Scoped, the same role reaches the three surfaces it administers.
		{"tenant_admin", &scope, http.MethodGet, "/admin/tenants", true},
		{"tenant_admin", &scope, http.MethodPost, "/admin/tenants", true},
		{"tenant_admin", &scope, http.MethodPut, "/admin/tenants/x", true},
		{"tenant_admin", &scope, http.MethodGet, "/admin/tenants/x/usage", true},
		{"tenant_admin", &scope, http.MethodGet, "/admin/api-keys", true},
		{"tenant_admin", &scope, http.MethodPost, "/admin/api-keys", true},
		{"tenant_admin", &scope, http.MethodDelete, "/admin/api-keys/x", true},
		{"tenant_admin", &scope, http.MethodGet, "/admin/model-quotas", true},
		{"tenant_admin", &scope, http.MethodPut, "/admin/model-quotas", true},
		// /admin/status is the deployment-wide dashboard: it carries no scope
		// to narrow, so a scoped administrator is refused it rather than shown
		// every tenant's traffic. The same reasoning already refused the trend
		// chart, which is the same numbers grouped by time.
		{"tenant_admin", &scope, http.MethodGet, "/admin/status", false},
		// Scoping a role is not a promotion: it opens exactly three surfaces,
		// not the whole control plane.
		{"tenant_admin", &scope, http.MethodGet, "/admin/providers", false},
		{"tenant_admin", &scope, http.MethodGet, "/admin/models", false},
		{"tenant_admin", &scope, http.MethodGet, "/admin/request-logs", false},
		{"tenant_admin", &scope, http.MethodGet, "/admin/users", false},
		{"tenant_admin", &scope, http.MethodGet, "/admin/admin-audit", false},
		{"tenant_admin", &scope, http.MethodGet, "/admin/security-events", false},
		{"tenant_admin", &scope, http.MethodGet, "/admin/status/trend", false},
		{"tenant_admin", &scope, http.MethodGet, "/admin/onboarding", false},
		{"operator", nil, http.MethodPost, "/admin/model-test", true},
		// A plain member reaches nothing but its own account.
		{"member", nil, http.MethodGet, "/admin/status", false},
		{"member", nil, http.MethodGet, "/admin/status/trend", false},
		{"member", nil, http.MethodGet, "/admin/request-logs", false},
		{"member", nil, http.MethodGet, "/admin/request-logs/summary", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		p := adminPrincipal{User: "u", Roles: map[string]bool{tc.role: true}, ScopeTenantID: tc.scope}
		if got := p.can(req); got != tc.want {
			t.Fatalf("%s %s as %s (scope %v) = %v, want %v", tc.method, tc.path, tc.role, tc.scope, got, tc.want)
		}
	}
}

// --- trusted-header identity ----------------------------------------------

// trustedHeaderConfig is the campus SSO configuration: a shared proxy secret
// plus the two identity headers.
func trustedHeaderConfig() Config {
	cfg := testConfig()
	cfg.AdminAuthMode = "trusted-header"
	cfg.AdminProxySecret = "phase1-proxy-secret"
	cfg.AdminUserHeader = "X-Forwarded-User"
	cfg.AdminRolesHeader = "X-Forwarded-Groups"
	return cfg
}

// trustedHeaderCall drives one request through the real middleware, answering
// 200 when the request is authorised. An empty roles value models a proxy that
// authenticated the user but mapped no group, which is the case P1-01 is about.
func trustedHeaderCall(t *testing.T, s *Server, roles, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	return trustedHeaderThrough(t, s, roles, method, path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

// trustedHeaderThrough is the same request with an explicit inner handler, for
// the cases where the response body is what is under test.
func trustedHeaderThrough(t *testing.T, s *Server, roles, method, path string, next http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://gateway.local"+path, nil)
	req.Header.Set("X-Request-Id", "test-req")
	req.Header.Set(s.cfg.AdminUserHeader, "zhang.san")
	if roles != "" {
		req.Header.Set(s.cfg.AdminRolesHeader, roles)
	}
	req.Header.Set("X-Campus-Auth-Secret", s.cfg.AdminProxySecret)
	rec := httptest.NewRecorder()
	s.adminMiddleware(next).ServeHTTP(rec, req)
	return rec
}

// P1-01. An authenticated identity with no usable group is an ordinary member.
//
// The previous behaviour granted auditor, which reads the global request log,
// the management audit trail and the security events — visibility nobody
// granted. The whoami endpoint stays reachable, because knowing who you are is
// not a privileged operation.
func TestTrustedHeaderWithoutRolesIsOnlyAMember(t *testing.T) {
	for _, tc := range []struct{ name, roles string }{
		{"no role header", ""},
		{"blank role header", "   "},
		{"unknown roles only", "god_mode,root"},
		{"legacy tenant_admin only", RoleTenantAdmin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestServer(t, trustedHeaderConfig())

			rec := trustedHeaderThrough(t, s, tc.roles, http.MethodGet, "/admin/whoami",
				http.HandlerFunc(s.handleAdminWhoAmI))
			if rec.Code != http.StatusOK {
				t.Fatalf("whoami must stay reachable, got %d: %s", rec.Code, rec.Body.String())
			}
			roles, _ := decodeBody(t, rec)["roles"].([]any)
			if len(roles) != 1 || roles[0] != RoleMember {
				t.Fatalf("roles = %v, want exactly [member]", roles)
			}

			for _, path := range []string{
				"/admin/status",
				"/admin/request-logs",
				"/admin/admin-audit",
				"/admin/security-events",
				"/admin/tenants",
				"/admin/api-keys",
				"/admin/providers",
				"/admin/users",
			} {
				if got := trustedHeaderCall(t, s, tc.roles, http.MethodGet, path); got.Code != http.StatusForbidden {
					t.Fatalf("GET %s with roles %q returned %d, want 403", path, tc.roles, got.Code)
				}
			}
		})
	}
}

// An explicit, known group keeps exactly the grant it had. The fix narrows the
// fallback; it must not narrow the roles a proxy deliberately assigns.
func TestTrustedHeaderExplicitRolesKeepTheirGrants(t *testing.T) {
	s, _ := newTestServer(t, trustedHeaderConfig())

	for _, tc := range []struct {
		roles  string
		method string
		path   string
		want   int
	}{
		{RoleAuditor, http.MethodGet, "/admin/request-logs", http.StatusOK},
		{RoleAuditor, http.MethodGet, "/admin/security-events", http.StatusOK},
		{RoleAuditor, http.MethodPost, "/admin/providers", http.StatusForbidden},
		{RoleGatewayAdmin, http.MethodPost, "/admin/providers", http.StatusOK},
		{RoleGatewayAdmin, http.MethodGet, "/admin/models", http.StatusOK},
		{RoleGatewayAdmin, http.MethodPut, "/admin/tenants/x", http.StatusForbidden},
		{RoleOperator, http.MethodGet, "/admin/status", http.StatusOK},
		{RoleOperator, http.MethodPost, "/admin/model-test", http.StatusOK},
		{RoleOperator, http.MethodGet, "/admin/models", http.StatusForbidden},
		{RoleSuperAdmin, http.MethodGet, "/admin/status", http.StatusOK},
		{RoleSuperAdmin, http.MethodGet, "/admin/users", http.StatusOK},
		// A combined header is normalised as one list, so both roles apply.
		{RoleGatewayAdmin + "," + RoleAuditor, http.MethodGet, "/admin/request-logs", http.StatusOK},
		{RoleGatewayAdmin + "," + RoleAuditor, http.MethodPost, "/admin/providers", http.StatusOK},
	} {
		if got := trustedHeaderCall(t, s, tc.roles, tc.method, tc.path); got.Code != tc.want {
			t.Fatalf("%s %s as %q = %d, want %d", tc.method, tc.path, tc.roles, got.Code, tc.want)
		}
	}
}

// The identity headers are not credentials. Without the shared secret they must
// not authenticate anything, and the absence of a role must never be what
// decides that — it is a separate question from whether the caller is trusted.
func TestTrustedHeaderForgedIdentityIsUnauthenticated(t *testing.T) {
	s, _ := newTestServer(t, trustedHeaderConfig())

	call := func(secret, user, roles string, setUser bool) int {
		req := httptest.NewRequest(http.MethodGet, "http://gateway.local/admin/whoami", nil)
		req.Header.Set("X-Request-Id", "test-req")
		if secret != "" {
			req.Header.Set("X-Campus-Auth-Secret", secret)
		}
		if setUser {
			req.Header.Set(s.cfg.AdminUserHeader, user)
		}
		if roles != "" {
			req.Header.Set(s.cfg.AdminRolesHeader, roles)
		}
		rec := httptest.NewRecorder()
		s.adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)
		return rec.Code
	}

	for _, tc := range []struct {
		name         string
		secret, user string
		setUser      bool
		roles        string
		want         int
	}{
		{"forged identity, no secret", "", "attacker", true, RoleSuperAdmin, http.StatusUnauthorized},
		{"wrong secret", "not-the-secret", "attacker", true, RoleSuperAdmin, http.StatusUnauthorized},
		{"correct secret, empty user", "phase1-proxy-secret", "", true, RoleSuperAdmin, http.StatusUnauthorized},
		{"correct secret, no user header", "phase1-proxy-secret", "", false, RoleAuditor, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := call(tc.secret, tc.user, tc.roles, tc.setUser); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("no proxy secret configured", func(t *testing.T) {
		cfg := trustedHeaderConfig()
		cfg.AdminProxySecret = ""
		unconfigured, _ := newTestServer(t, cfg)
		req := httptest.NewRequest(http.MethodGet, "http://gateway.local/admin/whoami", nil)
		req.Header.Set("X-Request-Id", "test-req")
		req.Header.Set(cfg.AdminUserHeader, "zhang.san")
		rec := httptest.NewRecorder()
		unconfigured.adminMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("an unconfigured proxy secret is a configuration error, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}
