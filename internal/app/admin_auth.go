package app

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// adminPrincipal is the authenticated caller. Roles live in a set because every
// authorisation check is a membership test.
//
// UserID and SessionID are empty for the legacy token and trusted-header modes.
type adminPrincipal struct {
	UserID    string
	Username  string
	User      string // display identity used in audit rows
	Roles     map[string]bool
	SessionID string
	AuthMode  string
}

type adminPrincipalKey struct{}

func withAdminPrincipal(ctx context.Context, p adminPrincipal) context.Context {
	return context.WithValue(ctx, adminPrincipalKey{}, p)
}

func adminPrincipalFrom(ctx context.Context) (adminPrincipal, bool) {
	p, ok := ctx.Value(adminPrincipalKey{}).(adminPrincipal)
	return p, ok
}

// authenticateAdmin resolves the request credential into a principal.
//
// In session mode the access token is read from the Authorization header and
// verified against the database on every request. The break-glass ADMIN_TOKEN is
// only honoured in token mode; session mode never consults it, so an operator
// cannot leave a bypass configured by accident.
func (s *Server) authenticateAdmin(r *http.Request) (adminPrincipal, error) {
	mode := strings.ToLower(strings.TrimSpace(s.cfg.AdminAuthMode))
	switch mode {
	case "session":
		return s.authenticateAdminSession(r)
	case "", "token":
		return s.authenticateAdminToken(r)
	case "trusted-header":
		return s.authenticateAdminTrustedHeader(r)
	default:
		return adminPrincipal{}, ErrAdminAuthDisabled
	}
}

func (s *Server) authenticateAdminSession(r *http.Request) (adminPrincipal, error) {
	if s.adminAuth == nil {
		return adminPrincipal{}, ErrAdminAuthDisabled
	}
	token := bearerValue(r)
	if token == "" {
		return adminPrincipal{}, ErrAdminUnauthenticated
	}
	return s.adminAuth.authenticate(r.Context(), token)
}

func (s *Server) authenticateAdminToken(r *http.Request) (adminPrincipal, error) {
	if s.cfg.AdminToken == "" {
		return adminPrincipal{}, ErrAdminAuthDisabled
	}
	token := bearerValue(r)
	if token == "" {
		return adminPrincipal{}, ErrAdminUnauthenticated
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AdminToken)) != 1 {
		return adminPrincipal{}, ErrAdminUnauthenticated
	}
	return adminPrincipal{
		User:     "admin_token",
		Roles:    map[string]bool{"super_admin": true},
		AuthMode: "token",
	}, nil
}

func (s *Server) authenticateAdminTrustedHeader(r *http.Request) (adminPrincipal, error) {
	if s.cfg.AdminProxySecret == "" {
		return adminPrincipal{}, ErrAdminAuthDisabled
	}
	got := strings.TrimSpace(r.Header.Get("X-Campus-Auth-Secret"))
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.AdminProxySecret)) != 1 {
		return adminPrincipal{}, ErrAdminUnauthenticated
	}
	user := strings.TrimSpace(r.Header.Get(s.cfg.AdminUserHeader))
	if user == "" {
		return adminPrincipal{}, ErrAdminUnauthenticated
	}
	roles := parseAdminRoles(r.Header.Get(s.cfg.AdminRolesHeader))
	if len(roles) == 0 {
		roles["auditor"] = true
	}
	return adminPrincipal{
		Username: user,
		User:     user,
		Roles:    roles,
		AuthMode: "trusted-header",
	}, nil
}

// bearerValue extracts a Bearer credential from the Authorization header.
func bearerValue(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
}

func parseAdminRoles(raw string) map[string]bool {
	out := map[string]bool{}
	raw = strings.NewReplacer(";", ",", " ", ",").Replace(raw)
	for _, item := range strings.Split(raw, ",") {
		role := strings.TrimSpace(item)
		switch role {
		case "super_admin", "gateway_admin", "tenant_admin", "auditor", "operator":
			out[role] = true
		}
	}
	return out
}

// can reports whether the principal may perform the request. super_admin is
// unrestricted; every other role is granted an explicit list of paths.
func (p adminPrincipal) can(r *http.Request) bool {
	if p.Roles["super_admin"] {
		return true
	}
	path := r.URL.Path
	method := r.Method

	// Self-service endpoints are always available to any signed-in account:
	// reading your own identity and rotating your own password must not depend
	// on RBAC.
	if p.canSelfService(path, method) {
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

// canSelfService covers the endpoints every authenticated administrator may use
// on their own account, independent of role.
func (p adminPrincipal) canSelfService(path, method string) bool {
	switch {
	case path == "/admin/whoami" && method == http.MethodGet:
		return true
	case path == "/admin/password" && method == http.MethodPost:
		return true
	case path == "/admin/logout" && method == http.MethodPost:
		return true
	case path == "/admin/auth/logout" && method == http.MethodPost:
		return true
	case path == "/admin/sessions" && method == http.MethodGet:
		return true
	case strings.HasPrefix(path, "/admin/sessions/") && strings.HasSuffix(path, "/revoke") && method == http.MethodPost:
		return true
	}
	return false
}

// --- client address resolution --------------------------------------------

// clientIPFrom resolves the caller address for audit and rate limiting.
//
// X-Forwarded-For is only honoured when the direct peer is one of the
// configured TRUSTED_PROXY_CIDRS. Otherwise any client could spoof the header
// and defeat per-IP login throttling. It replaces the previous behaviour of
// trusting the header unconditionally.
func (s *Server) clientIPFrom(r *http.Request) string {
	peer := remoteHost(r.RemoteAddr)
	if !s.isTrustedProxy(peer) {
		return peer
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return peer
	}
	// Walk the chain from the closest hop backwards and return the first
	// address that is not itself a trusted proxy.
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		if !s.isTrustedProxy(candidate) {
			return candidate
		}
	}
	return peer
}

func (s *Server) isTrustedProxy(ip string) bool {
	if len(s.cfg.TrustedProxyCIDRs) == 0 || ip == "" {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, entry := range s.cfg.TrustedProxyCIDRs {
		ipNet, _, err := parseCIDROrIP(entry)
		if err != nil {
			continue
		}
		if ipNet.Contains(parsed) {
			return true
		}
	}
	return false
}

func remoteHost(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// isLoopbackRequest reports whether the request originated on the local machine.
// It is used to gate first-run setup. A trusted proxy is not enough: the whole
// point is that the operator must be sitting at the machine.
func isLoopbackRequest(r *http.Request) bool {
	host := remoteHost(r.RemoteAddr)
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
