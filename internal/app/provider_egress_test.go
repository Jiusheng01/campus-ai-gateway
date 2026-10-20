package app

import (
	"net"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// providerWithBaseURL builds a minimal valid channel whose only interesting
// field is the target under test.
func providerWithBaseURL(baseURL string) model.Provider {
	return model.Provider{
		Name:          "qwen",
		BaseURL:       baseURL,
		Protocol:      "openai-chat",
		CredentialEnv: "QWEN_API_KEY",
	}
}

// A Provider target is a fixed prefix, not an arbitrary URL: no userinfo, no
// query, no fragment, and no path that can be read as anything but a prefix.
// Each violation gets its own reason so the operator knows what to change.
func TestProviderBaseURLRejectsMalformedTargets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "relative URL", baseURL: "/v1", want: "absolute URL"},
		{name: "no scheme", baseURL: "api.example.com/v1", want: "absolute URL"},
		{name: "unsupported scheme", baseURL: "ftp://api.example.com/v1", want: "http:// or https://"},
		{name: "userinfo", baseURL: "https://operator:secret@api.example.com/v1", want: "userinfo"},
		{name: "query", baseURL: "https://api.example.com/v1?x=1", want: "query"},
		{name: "trailing question mark", baseURL: "https://api.example.com/v1?", want: "query"},
		{name: "fragment", baseURL: "https://api.example.com/v1#frag", want: "fragment"},
		{name: "trailing hash", baseURL: "https://api.example.com/v1#", want: "fragment"},
		{name: "empty host", baseURL: "https:///v1", want: "host"},
		{name: "dot segment", baseURL: "https://api.example.com/v1/../admin", want: "path"},
		{name: "encoded slash", baseURL: "https://api.example.com/v1%2fadmin", want: "path"},
		{name: "encoded dot segment", baseURL: "https://api.example.com/%2e%2e/admin", want: "path"},
		{name: "backslash", baseURL: `https://api.example.com/v1\admin`, want: "path"},
		{name: "encoded control character", baseURL: "https://api.example.com/v1%00admin", want: "path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := providerWithBaseURL(tc.baseURL)
			reason := validateProvider(&v)
			if reason == "" {
				t.Fatalf("base_url %q must be refused", tc.baseURL)
			}
			if !strings.Contains(strings.ToLower(reason), strings.ToLower(tc.want)) {
				t.Fatalf("base_url %q was refused with %q, which does not name %q", tc.baseURL, reason, tc.want)
			}
		})
	}
}

// A fixed prefix is the whole path grammar: the host plus an optional path,
// port and IPv6 literal included.
func TestProviderBaseURLAcceptsHTTPSPrefixes(t *testing.T) {
	for _, baseURL := range []string{
		"https://api.example.com",
		"https://api.example.com/v1",
		"https://api.example.com:8443/v1/prefix",
		"https://[2001:db8::1]:8443/v1",
	} {
		t.Run(baseURL, func(t *testing.T) {
			v := providerWithBaseURL(baseURL)
			if reason := validateProvider(&v); reason != "" {
				t.Fatalf("base_url %q must be accepted: %s", baseURL, reason)
			}
		})
	}
}

// Plaintext is the exception, not the default: an empty allowlist rejects every
// http:// target, and a matching entry is the only thing that opens one.
func TestProviderBaseURLRejectsHTTPWithoutAnAllowlistEntry(t *testing.T) {
	v := providerWithBaseURL("http://upstream.internal/v1")
	reason := validateProvider(&v)
	if reason == "" {
		t.Fatal("http:// must be refused when no allowlist is configured")
	}
	if !strings.Contains(reason, "PROVIDER_HTTP_ALLOWLIST") {
		t.Fatalf("the refusal must name the allowlist, got %q", reason)
	}
}

// The allowlist matches an exact host (case-insensitively) or an address inside
// a CIDR. A hostname entry does not silently widen to "anything that resolves
// near it".
func TestProviderHTTPAllowlistMatchesHostsAndCIDRs(t *testing.T) {
	allow, err := parseHostAllowlist([]string{"127.0.0.1", "10.0.0.0/8", "Campus-Proxy.Example.EDU"})
	if err != nil {
		t.Fatalf("parseHostAllowlist: %v", err)
	}

	for _, baseURL := range []string{
		"http://127.0.0.1:8080/v1",
		"http://10.20.30.40/v1",
		"http://campus-proxy.example.edu/v1",
	} {
		if reason := validateProviderBaseURL(baseURL, allow); reason != "" {
			t.Fatalf("%q must be allowlisted: %s", baseURL, reason)
		}
	}

	for _, baseURL := range []string{
		"http://192.168.1.10/v1",
		"http://public.example.com/v1",
		"http://campus-proxy.example.edu.evil.test/v1",
	} {
		if reason := validateProviderBaseURL(baseURL, allow); reason == "" {
			t.Fatalf("%q must not be allowlisted", baseURL)
		}
	}

	// https never needs the allowlist.
	if reason := validateProviderBaseURL("https://public.example.com/v1", allow); reason != "" {
		t.Fatalf("https must not depend on the allowlist: %s", reason)
	}
}

// A CIDR entry also covers a hostname at write time, via its resolved
// addresses. The lookup is injected so the rule is tested without asking the
// machine's resolver.
func TestProviderHTTPAllowlistMatchesAResolvedHostInCIDR(t *testing.T) {
	allow, err := parseHostAllowlist([]string{"203.0.113.0/24"})
	if err != nil {
		t.Fatalf("parseHostAllowlist: %v", err)
	}
	allow.lookupIP = func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.9")}, nil
	}
	if reason := validateProviderBaseURL("http://campus-llm.example.edu/v1", allow); reason != "" {
		t.Fatalf("a host resolving into the allowlisted CIDR must be accepted: %s", reason)
	}

	allow.lookupIP = func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("198.51.100.9")}, nil
	}
	if reason := validateProviderBaseURL("http://campus-llm.example.edu/v1", allow); reason == "" {
		t.Fatal("a host resolving outside the allowlisted CIDR must be refused")
	}
}

// The write handlers validate through the Server, which is where the deployed
// allowlist lives. A Server without one keeps the fail-closed rule; a Server
// configured with the exception accepts exactly the http:// target it names.
func TestServerProviderValidationUsesConfiguredAllowlist(t *testing.T) {
	strict := &Server{}
	v := providerWithBaseURL("http://127.0.0.1:8080/v1")
	if reason := strict.validateProvider(&v); reason == "" {
		t.Fatal("a Server with no allowlist must refuse http://")
	}

	allowed := &Server{cfg: Config{ProviderHTTPAllowlist: []string{"127.0.0.1"}}}
	httpTarget := providerWithBaseURL("http://127.0.0.1:8080/v1")
	if reason := allowed.validateProvider(&httpTarget); reason != "" {
		t.Fatalf("an allowlisted http:// target must be accepted on create: %s", reason)
	}
	httpUpdate := providerWithBaseURL("http://127.0.0.1:8080/v1")
	if reason := allowed.validateProviderForUpdate(&httpUpdate); reason != "" {
		t.Fatalf("an allowlisted http:// target must be accepted on update: %s", reason)
	}

	other := providerWithBaseURL("http://127.0.0.2:8080/v1")
	if reason := allowed.validateProvider(&other); reason == "" {
		t.Fatal("a Server must not accept an http:// host that is not allowlisted")
	}
}

// create and update share the rule: a malformed target cannot slip in by
// editing an existing channel instead of creating one.
func TestProviderBaseURLRuleAppliesToCreateAndUpdate(t *testing.T) {
	for write, validate := range map[string]func(*model.Provider) string{
		"create": validateProvider,
		"update": validateProviderForUpdate,
	} {
		for _, baseURL := range []string{
			"http://upstream.internal/v1",
			"https://operator:secret@api.example.com/v1",
			"https://api.example.com/v1?x=1",
			"ftp://api.example.com/v1",
			"https:///v1",
		} {
			t.Run(write+"/"+baseURL, func(t *testing.T) {
				v := providerWithBaseURL(baseURL)
				if reason := validate(&v); reason == "" {
					t.Fatalf("%s must refuse %q", write, baseURL)
				}
			})
		}
	}
}
