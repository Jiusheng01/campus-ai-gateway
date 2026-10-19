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
	// The shared secret is the whole of the authentication in this mode, so
	// the peer address is checked before it: a caller outside ADMIN_PROXY_CIDRS
	// is refused without the secret ever being compared, which keeps an
	// internet-facing port from turning a leaked string into an administrator.
	//
	// An empty list keeps the historical behaviour of accepting any peer. That
	// is the compatible choice, not the safe one, and startup says so.
	if len(s.cfg.AdminProxyCIDRs) > 0 && !s.peerInAdminProxyCIDRs(r) {
		return adminPrincipal{}, ErrAdminUnauthenticated
	}
	got := strings.TrimSpace(r.Header.Get("X-Campus-Auth-Secret"))
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.AdminProxySecret)) != 1 {
		return adminPrincipal{}, ErrAdminUnauthenticated
	}
	user := strings.TrimSpace(r.Header.Get(s.cfg.AdminUserHeader))
	if user == "" {
		return adminPrincipal{}, ErrAdminUnauthenticated
	}
	// A verified identity whose groups map to no global role is a plain member.
	//
	// This used to default to auditor, which reads the global request log, the
	// management audit trail and the security events. An authenticated but
	// unmapped user was therefore handed visibility nobody granted. A missing or
	// unrecognised group is a mapping gap, and a mapping gap must resolve to the
	// least privilege, not to read access over the whole control plane.
	roles := parseAdminRoles(r.Header.Get(s.cfg.AdminRolesHeader))
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

// parseAdminRoles parses the role list a campus proxy injected and returns it in
// the same canonical form every other role path produces, so the proxy path
// cannot end up reading the same list differently from a stored one.
//
// A group that maps to no global privilege is dropped rather than kept. Holding
// a role that grants nothing would leave the principal's identity disagreeing
// with what it may actually do, and "member" is the accurate description of an
// account with no management grant. Unknown groups are dropped for the reason
// they always were: a typo must not sit in a role set looking like a permission.
//
// The result is never empty — an account always has a role — and it is never
// "auditor". Missing or unrecognised groups must not resolve to privilege.
func parseAdminRoles(raw string) map[string]bool {
	raw = strings.NewReplacer(";", ",", " ", ",").Replace(raw)
	parts := make([]string, 0, 4)
	for _, item := range strings.Split(raw, ",") {
		role := strings.TrimSpace(item)
		if role == RoleMember || IsManagementRole(role) {
			parts = append(parts, role)
		}
	}
	return roleSet(parts)
}

// can reports whether the principal may perform the request. super_admin is
// unrestricted; every other role is granted an explicit list of paths.
//
// This is an allow-list, and that is what keeps the user management API safe:
// /admin/users is reachable only through the super_admin branch above, so no
// other role — in particular not gateway_admin — can grant itself a role.
func (p adminPrincipal) can(r *http.Request) bool {
	if p.Roles[RoleSuperAdmin] {
		return true
	}
	path := r.URL.Path
	method := r.Method

	// Self-service endpoints are always available to any signed-in account:
	// reading your own identity, rotating your own password and managing your
	// own devices must not depend on RBAC. This is also the whole of what a
	// plain member may do — everything below is an explicit management grant.
	if p.canSelfService(path, method) {
		return true
	}

	if p.Roles[RoleAuditor] && method == http.MethodGet {
		switch {
		case path == "/admin/status",
			// The trend is the same telemetry /admin/status already returns,
			// grouped by time, so a role that may read the status page may read
			// the chart of it. It is named explicitly rather than covered by a
			// prefix rule on /admin/status so a future endpoint under that path
			// has to justify its own grant.
			path == "/admin/status/trend",
			strings.HasPrefix(path, "/admin/request-logs"),
			// The shadow report is routing evidence read from the same request
			// and attempt records the log endpoints already expose, so it is the
			// same read-only grant.
			path == "/admin/shadow-report",
			path == "/admin/admin-audit",
			path == "/admin/security-events":
			return true
		}
	}
	if p.Roles[RoleOperator] {
		if method == http.MethodGet && (path == "/admin/status" || path == "/admin/status/trend") {
			return true
		}
		// The connectivity test is a diagnostic, not an edit: an operator may
		// run it and pick a model from the narrowed option list, but may not
		// read or change the model configuration itself.
		if method == http.MethodGet && path == "/admin/model-test-options" {
			return true
		}
		if method == http.MethodPost && path == "/admin/model-test" {
			return true
		}
	}
	if p.Roles[RoleGatewayAdmin] {
		switch {
		case strings.HasPrefix(path, "/admin/providers"),
			strings.HasPrefix(path, "/admin/models"),
			strings.HasPrefix(path, "/admin/model-routes"),
			path == "/admin/thinkwatch/sync",
			path == "/admin/model-test",
			path == "/admin/model-test-options",
			path == "/admin/status",
			path == "/admin/status/trend":
			return true
		}
	}
	// There is deliberately no tenant_admin branch. Its global form granted
	// every tenant and every API key with no resource scope, so a holder could
	// read and rewrite any tenant's credentials. The confirmed design puts
	// tenant_admin *inside* a tenant, and until that membership model exists
	// there is no correct scope to grant — so the role grants nothing rather
	// than everything. An account still carrying the value is administratively
	// inert and can be reassigned by a super administrator.
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
	return s.ipInCIDRs(ip, s.cfg.TrustedProxyCIDRs)
}

// peerInAdminProxyCIDRs reports whether the direct peer may use trusted-header
// authentication. It deliberately looks at RemoteAddr and not at any forwarding
// header: the header is written by the caller, and the point of the list is to
// be the one thing the caller cannot choose.
func (s *Server) peerInAdminProxyCIDRs(r *http.Request) bool {
	peer := remoteHost(r.RemoteAddr)
	if peer == "" {
		return false
	}
	return s.ipInCIDRs(peer, s.cfg.AdminProxyCIDRs)
}

func (s *Server) ipInCIDRs(ip string, cidrs []string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, entry := range cidrs {
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

// isLocalSetupRequest decides whether first-run setup may run for this request.
//
// The check deliberately does not ask "is the peer loopback". A reverse proxy
// running on the same host also connects from 127.0.0.1, so a peer-only check
// would let a remote visitor create the first super administrator through the
// proxy. The decision uses the resolved client address instead, and fails
// closed whenever forwarding headers arrive from a peer that is not a
// configured trusted proxy.
//
//  1. Loopback peer, no forwarding headers      -> local
//  2. Trusted proxy peer, resolved client is
//     loopback                                    -> local
//  3. Trusted proxy peer, resolved client is
//     public or LAN                               -> rejected
//  4. Loopback peer with forwarding headers but
//     not a configured trusted proxy              -> rejected (fail closed)
//  5. Any other peer (remote direct, or a
//     non-trusted remote proxy)                   -> rejected
func (s *Server) isLocalSetupRequest(r *http.Request) bool {
	peer := remoteHost(r.RemoteAddr)
	peerIP := net.ParseIP(peer)
	peerIsLoopback := peerIP != nil && peerIP.IsLoopback()
	peerIsTrusted := s.isTrustedProxy(peer)

	// Rule 5: a direct connection from anywhere other than loopback is never
	// local. A remote proxy we were not told to trust is treated the same way.
	if !peerIsLoopback && !peerIsTrusted {
		return false
	}

	if hasForwardingHeaders(r) {
		// Rule 4: forwarding headers are only meaningful from a proxy we were
		// explicitly configured to trust. A loopback peer that is not in
		// TRUSTED_PROXY_CIDRS could be any local process, so refuse rather than
		// guess at the client address.
		if !peerIsTrusted {
			return false
		}
		// Rules 2 and 3: trust the resolved client address, not the hop.
		clientIP := net.ParseIP(s.clientIPFrom(r))
		return clientIP != nil && clientIP.IsLoopback()
	}

	// Rule 1: no forwarding headers, so the peer address is the client address.
	return peerIsLoopback
}

// hasForwardingHeaders reports whether the request carries any header a proxy
// would use to describe the original client.
func hasForwardingHeaders(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("X-Forwarded-For")) != "" ||
		strings.TrimSpace(r.Header.Get("Forwarded")) != "" ||
		strings.TrimSpace(r.Header.Get("X-Real-IP")) != ""
}
