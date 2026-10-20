package app

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// hostAllowlist is the parsed form of PROVIDER_HTTP_ALLOWLIST: exact hostnames
// plus address ranges. It is the one predicate for "is this target allowed to
// speak plaintext HTTP", shared by write-time validation and, later, by the
// dial-time address check that sees the resolved IP.
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
		if r < 0x20 || r == 0x7f {
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
