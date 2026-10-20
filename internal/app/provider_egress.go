package app

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
)

// egressPolicy is the dial-time half of ADR-0012.
//
// It is applied from net.Dialer.Control, which runs after the resolver has
// produced an address and before the connection is made. That position is the
// point: every new connection is checked against the address it is actually
// about to reach, so a name that resolves to a public address once and a
// private one later (DNS rebinding) does not get a second, unexamined chance.
//
// The zero value is strict. NewServer constructs it explicitly, and tests that
// need a local upstream inject the permissive value through their own helper.
// There is deliberately no environment variable or Config field for this: a
// switch that can weaken egress policy is the vulnerability, not the escape
// hatch.
type egressPolicy struct {
	allowDangerous bool
}

// strictEgressPolicy denies every address ADR-0012 calls dangerous.
func strictEgressPolicy() egressPolicy {
	return egressPolicy{}
}

// permissiveEgressPolicy is the test-only injection. httptest upstreams listen
// on 127.0.0.1, which the strict policy correctly refuses.
func permissiveEgressPolicy() egressPolicy {
	return egressPolicy{allowDangerous: true}
}

// control is the net.Dialer.Control hook. address is "ip:port": the resolver
// has already run, so this sees the real target of this connection.
func (p egressPolicy) control(_, address string, _ syscall.RawConn) error {
	if p.allowDangerous {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return &egressDeniedError{Address: address, Reason: "the dial address is not a host:port"}
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return &egressDeniedError{Address: address, Reason: "the dial address is not an IP address"}
	}
	if reason := deniedAddressReason(ip); reason != "" {
		return &egressDeniedError{Address: host, Reason: reason}
	}
	return nil
}

// cloudMetadataAddresses are the instance-metadata endpoints that hand out
// credentials. Most are link-local and would be refused anyway; naming them
// explicitly keeps the reason accurate and covers the ones that are not.
var cloudMetadataAddresses = []net.IP{
	net.ParseIP("169.254.169.254"), // AWS, GCP, Azure, OpenStack
	net.ParseIP("fd00:ec2::254"),   // AWS IMDS over IPv6
	net.ParseIP("100.100.100.200"), // Alibaba Cloud
}

// deniedAddressReason names the first rule an address violates, or "" when the
// address is an acceptable egress target.
func deniedAddressReason(ip net.IP) string {
	for _, metadata := range cloudMetadataAddresses {
		if ip.Equal(metadata) {
			return "cloud metadata address"
		}
	}
	switch {
	case ip.IsLoopback():
		return "loopback address"
	case ip.IsLinkLocalUnicast():
		return "link-local unicast address"
	case ip.IsLinkLocalMulticast():
		return "link-local multicast address"
	case ip.IsMulticast():
		return "multicast address"
	case ip.IsUnspecified():
		return "unspecified address"
	case isIPv4ZeroNetwork(ip):
		// On Linux 0.0.0.0/8 routes to local interfaces, so the whole range is
		// a local-service target; only the exact 0.0.0.0 is IsUnspecified.
		return "0.0.0.0/8 address"
	}
	return ""
}

// isIPv4ZeroNetwork reports whether ip is in 0.0.0.0/8. To4 also normalises an
// IPv4-mapped IPv6 address, so ::ffff:0.1.2.3 is covered by the same rule.
func isIPv4ZeroNetwork(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 0
}

// egressDeniedError is the recognisable refusal the dialer returns. The dial
// hook fills Address and Reason; the data plane adds Channel once it knows
// which Provider the connection was for, so the failure can name both without
// the transport knowing about channels.
type egressDeniedError struct {
	Channel string
	Address string
	Reason  string
}

func (e *egressDeniedError) Error() string {
	target := e.Address
	if target == "" {
		target = "the provider target"
	}
	if e.Channel != "" {
		return fmt.Sprintf("channel %s: egress to %s is denied by policy: %s", e.Channel, target, e.Reason)
	}
	return fmt.Sprintf("egress to %s is denied by policy: %s", target, e.Reason)
}

// SafeSummary is the caller-facing form of a denial: the channel and the policy
// reason, never the resolved address. ADR-0009 keeps network detail in the
// operational log and the security event; the response only gets a stable code
// and a fixed summary an operator can act on.
func (e *egressDeniedError) SafeSummary() string {
	if e.Channel != "" {
		return fmt.Sprintf("channel %s: egress to the provider target is denied by policy: %s", e.Channel, e.Reason)
	}
	return "egress to the provider target is denied by policy: " + e.Reason
}

// egressTargetReason applies the scheme and plaintext-allowlist rules to the
// URL about to be dialled. Dialer.Control only receives an ip:port, so the
// scheme is not visible there; this is the same egress decision, made one step
// earlier on the target URL.
//
// Write-time validation is not enough on its own: a row can predate the rule,
// and a hostname that was allowlisted when it was saved can resolve elsewhere
// later. A refusal here is the same typed egressDeniedError the address policy
// returns, so every audience treats the two identically.
func (s *Server) egressTargetReason(target string) string {
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" {
		return "the provider target is not a valid absolute URL"
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return ""
	case "http":
		if allowlistAllowsHost(s.providerHTTPAllowlist(), u.Hostname()) {
			return ""
		}
		return fmt.Sprintf("plaintext http:// to %q is not in PROVIDER_HTTP_ALLOWLIST", u.Hostname())
	default:
		return fmt.Sprintf("provider target scheme %q is not http or https", u.Scheme)
	}
}

// hostAllowlist is the parsed form of PROVIDER_HTTP_ALLOWLIST: exact hostnames
// plus address ranges. It is the one predicate for "is this target allowed to
// speak plaintext HTTP", shared by write-time validation and by the request-time
// target check that runs before the dialer. The address policy is separate: it
// denies dangerous resolved addresses even when the allowlist matched.
type hostAllowlist struct {
	hosts map[string]struct{}
	nets  []*net.IPNet

	// lookupIP is a seam for tests. Production leaves it nil and uses the
	// system resolver.
	lookupIP func(string) ([]net.IP, error)
}

// parseHostAllowlist turns comma-separated entries into the predicate. Each
// entry is a hostname, an IP address, or a CIDR block; anything else is a
// configuration error that must stop startup rather than silently match
// nothing.
func parseHostAllowlist(entries []string) (*hostAllowlist, error) {
	allow := &hostAllowlist{hosts: make(map[string]struct{})}
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR block %q", entry)
			}
			allow.nets = append(allow.nets, network)
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			allow.nets = append(allow.nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		host, ok := normalizeAllowlistHost(entry)
		if !ok {
			return nil, fmt.Errorf("invalid hostname %q", entry)
		}
		allow.hosts[host] = struct{}{}
	}
	return allow, nil
}

// normalizeAllowlistHost accepts the RFC 1123 hostname shape. Rejecting a
// scheme, port, path or wildcard here is deliberate: the operator should write
// the host the way the gateway will read it from base_url, and a wildcard is a
// policy decision this variable does not express.
func normalizeAllowlistHost(raw string) (string, bool) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if host == "" || len(host) > 253 {
		return "", false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
				continue
			}
			return "", false
		}
	}
	return host, true
}

// AllowsHost reports whether base_url's host is allowlisted. A literal address
// is checked against the ranges; a hostname must match an entry exactly or
// resolve into one of them. Resolution failure is not an allow.
func (a *hostAllowlist) AllowsHost(host string) bool {
	if a == nil {
		return false
	}
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return false
	}
	if _, ok := a.hosts[host]; ok {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return a.AllowsIP(ip)
	}
	if len(a.nets) == 0 {
		return false
	}
	lookup := a.lookupIP
	if lookup == nil {
		lookup = net.LookupIP
	}
	ips, err := lookup(host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if a.AllowsIP(ip) {
			return true
		}
	}
	return false
}

// AllowsIP is the address half of the predicate, kept separate so the dial-time
// check can reuse it on a resolved address.
func (a *hostAllowlist) AllowsIP(ip net.IP) bool {
	if a == nil || ip == nil {
		return false
	}
	for _, network := range a.nets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// validateProviderBaseURL is the write-time rule for a Provider target.
//
// A Provider target is a fixed prefix — scheme, host, optional port and optional
// path — not an arbitrary URL. Anything that could make the same string mean two
// different things to the gateway and to the upstream is refused while the
// operator is still looking at the form: userinfo can smuggle a credential into
// logs, a query or fragment contradicts the fixed-prefix contract, and encoded
// separators or dot segments turn a prefix into a path traversal.
//
// Plaintext http:// is refused unless the host is in allowlist. The dial-time
// address policy is a separate line of defence (ADR-0012); this one exists so a
// malformed target never reaches the database in the first place.
func validateProviderBaseURL(raw string, allowlist *hostAllowlist) string {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() {
		return "base_url must be a valid absolute URL"
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Sprintf("base_url must use http:// or https:// (got %q)", u.Scheme)
	}
	if u.User != nil {
		return "base_url must not contain userinfo (user:password@)"
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "base_url must not contain a query string"
	}
	// A trailing "#" leaves Fragment empty but is still a fragment delimiter,
	// which is why the raw string is consulted as well.
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return "base_url must not contain a fragment"
	}
	host := u.Hostname()
	if host == "" {
		return "base_url must include a host"
	}
	if reason := validateProviderBaseURLPath(u); reason != "" {
		return reason
	}
	if scheme == "http" && !allowlistAllowsHost(allowlist, host) {
		return fmt.Sprintf("base_url uses http:// but %q is not in PROVIDER_HTTP_ALLOWLIST; use https:// or allowlist the host", host)
	}
	return ""
}

// validateProviderBaseURLPath guards the "fixed prefix" part of the target.
//
// url.Parse leaves dot segments in place rather than cleaning them, and encoded
// separators/periods mean a proxy and the gateway can disagree about which path
// is being requested. Rejecting them is the only reading that keeps the prefix
// fixed.
func validateProviderBaseURLPath(u *url.URL) string {
	escaped := u.EscapedPath()
	if strings.ContainsAny(escaped, " \t\r\n") {
		return "base_url path must not contain whitespace or control characters"
	}
	// Escaped forms (%00, %0a) decode into u.Path, so the decoded path is
	// checked too rather than trusting the raw bytes alone.
	for _, r := range u.Path {
		// Space (0x20) is whitespace too: leaving it out let "https://h/v 1"
		// through, and a path the request writer and the upstream read
		// differently is exactly what the fixed-prefix rule exists to prevent.
		if r <= 0x20 || r == 0x7f {
			return "base_url path must not contain whitespace or control characters"
		}
	}
	lower := strings.ToLower(escaped)
	for _, marker := range []string{"%2f", "%5c", "%2e"} {
		if strings.Contains(lower, marker) {
			return "base_url path must be a fixed prefix without encoded separators or dot segments"
		}
	}
	if strings.Contains(u.Path, `\`) {
		return "base_url path must be a fixed prefix without backslashes"
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return "base_url path must be a fixed prefix without '.' or '..' segments"
		}
	}
	return ""
}

// allowlistAllowsHost is nil-safe so validation without a configured allowlist
// still means "reject every http:// target".
func allowlistAllowsHost(allowlist *hostAllowlist, host string) bool {
	if allowlist == nil {
		return false
	}
	return allowlist.AllowsHost(host)
}
