package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

type adminPrincipal struct {
	User  string
	Roles map[string]bool
}

type adminPrincipalKey struct{}

func withAdminPrincipal(ctx context.Context, p adminPrincipal) context.Context {
	return context.WithValue(ctx, adminPrincipalKey{}, p)
}

func adminPrincipalFrom(ctx context.Context) (adminPrincipal, bool) {
	p, ok := ctx.Value(adminPrincipalKey{}).(adminPrincipal)
	return p, ok
}

func (s *Server) authenticateAdmin(r *http.Request) (adminPrincipal, error) {
	switch strings.ToLower(strings.TrimSpace(s.cfg.AdminAuthMode)) {
	case "", "token":
		if s.cfg.AdminToken == "" {
			return adminPrincipal{}, errors.New("admin api disabled")
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			return adminPrincipal{}, errors.New("missing admin bearer token")
		}
		got := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.AdminToken)) != 1 {
			return adminPrincipal{}, errors.New("invalid admin token")
		}
		return adminPrincipal{
			User:  "admin_token",
			Roles: map[string]bool{"super_admin": true},
		}, nil

	case "session":
		// Local username/password accounts. A static ADMIN_TOKEN, when
		// configured, keeps working as a break-glass credential so a damaged
		// account table can never lock an operator out of the control plane.
		raw := bearerValue(r)
		if raw == "" {
			return adminPrincipal{}, errors.New("missing admin session")
		}
		if s.cfg.AdminToken != "" && subtle.ConstantTimeCompare([]byte(raw), []byte(s.cfg.AdminToken)) == 1 {
			return adminPrincipal{
				User:  "admin_token",
				Roles: map[string]bool{"super_admin": true},
			}, nil
		}
		sess, ok := s.lookupAdminSession(r.Context(), raw)
		if !ok {
			return adminPrincipal{}, errors.New("invalid admin session")
		}
		// Roles and status come from the database rather than from the session
		// payload, so disabling or demoting an account takes effect on the very
		// next request instead of at session expiry.
		user, err := s.pg.FindAdminUserByID(r.Context(), sess.UserID)
		if err != nil {
			s.revokeAdminSession(r.Context(), raw)
			return adminPrincipal{}, errors.New("invalid admin session")
		}
		if user.Status != 1 {
			s.revokeAdminSession(r.Context(), raw)
			return adminPrincipal{}, errors.New("administrator account is disabled")
		}
		// A password change rewrites the stored hash, so any session issued
		// with the previous password stops matching and is dropped here.
		if sess.PasswordTag != passwordTag(user.PasswordHash) {
			s.revokeAdminSession(r.Context(), raw)
			return adminPrincipal{}, errors.New("administrator password changed")
		}
		roles := filterAdminRoles(user.Roles)
		if len(roles) == 0 {
			roles["auditor"] = true
		}
		return adminPrincipal{User: user.Username, Roles: roles}, nil

	case "trusted-header":
		if s.cfg.AdminProxySecret == "" {
			return adminPrincipal{}, errors.New("ADMIN_PROXY_SECRET is required in trusted-header mode")
		}
		got := strings.TrimSpace(r.Header.Get("X-Campus-Auth-Secret"))
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.AdminProxySecret)) != 1 {
			return adminPrincipal{}, errors.New("untrusted admin auth proxy")
		}
		user := strings.TrimSpace(r.Header.Get(s.cfg.AdminUserHeader))
		if user == "" {
			return adminPrincipal{}, errors.New("authenticated user header is missing")
		}
		roles := parseAdminRoles(r.Header.Get(s.cfg.AdminRolesHeader))
		if len(roles) == 0 {
			roles["auditor"] = true
		}
		return adminPrincipal{User: user, Roles: roles}, nil
	default:
		return adminPrincipal{}, errors.New("unsupported ADMIN_AUTH_MODE")
	}
}

func parseAdminRoles(raw string) map[string]bool {
	raw = strings.NewReplacer(";", ",", " ", ",").Replace(raw)
	return filterAdminRoles(strings.Split(raw, ","))
}

// filterAdminRoles drops anything outside the known role vocabulary, so a typo
// in the account table degrades to "no permissions" rather than to an unknown
// role that silently fails every authorisation check.
func filterAdminRoles(items []string) map[string]bool {
	out := map[string]bool{}
	for _, item := range items {
		role := strings.TrimSpace(item)
		switch role {
		case "super_admin", "gateway_admin", "tenant_admin", "auditor", "operator":
			out[role] = true
		}
	}
	return out
}

func (p adminPrincipal) can(r *http.Request) bool {
	if p.Roles["super_admin"] {
		return true
	}
	path := r.URL.Path
	method := r.Method
	if path == "/admin/whoami" && method == http.MethodGet {
		return true
	}

	if p.Roles["auditor"] && method == http.MethodGet {
		switch {
		case path == "/admin/status",
			strings.HasPrefix(path, "/admin/request-logs"),
			path == "/admin/admin-audit",
			path == "/admin/security-events":
			return true
		}
	}
	if p.Roles["operator"] {
		if method == http.MethodGet && path == "/admin/status" {
			return true
		}
		if method == http.MethodPost && path == "/admin/model-test" {
			return true
		}
	}
	if p.Roles["gateway_admin"] {
		switch {
		case strings.HasPrefix(path, "/admin/providers"),
			strings.HasPrefix(path, "/admin/models"),
			strings.HasPrefix(path, "/admin/model-routes"),
			path == "/admin/thinkwatch/sync",
			path == "/admin/model-test",
			path == "/admin/status":
			return true
		}
	}
	if p.Roles["tenant_admin"] {
		switch {
		case strings.HasPrefix(path, "/admin/tenants"),
			strings.HasPrefix(path, "/admin/api-keys"),
			path == "/admin/status":
			return true
		}
	}
	return false
}
