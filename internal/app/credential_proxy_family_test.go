package app

import (
	"strings"
	"testing"
)

// The direct data plane builds its outbound transport with
// http.ProxyFromEnvironment, which reads HTTP_PROXY, HTTPS_PROXY, NO_PROXY and
// ALL_PROXY. Those variables frequently embed "user:password@proxy", so a
// channel credential_env that names one of them hands the operator the proxy
// credential - the same escalation the namespace rule exists to prevent.
//
// The existing family test iterates reservedCredentialEnvPrefixes, so it can
// only catch families someone remembered to list. This test names the proxy
// family members directly, so a prefix table that forgets one fails here.
func TestCredentialEnvRefusesTheProxyFamily(t *testing.T) {
	for _, name := range []string{
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"NO_PROXY",
		"ALL_PROXY",
		"http_proxy",
		"https_proxy",
		"no_proxy",
		"all_proxy",
	} {
		t.Run(name, func(t *testing.T) {
			// Lower-case forms are already refused by the shape rule, but they
			// are asserted here too: the requirement is "this name is not a
			// channel credential", not "this name was refused by some rule".
			if reason := validateCredentialEnv(name); reason == "" {
				t.Fatalf("%s must be refused: ProxyFromEnvironment reads it", name)
			}
		})
	}
}

// The refusal must still tell the operator the gateway owns the name, so they
// know to create a dedicated variable rather than renaming it slightly.
func TestCredentialEnvNamesTheProxyFamilyAsGatewayOwned(t *testing.T) {
	for _, name := range []string{"HTTPS_PROXY", "NO_PROXY", "ALL_PROXY"} {
		reason := validateCredentialEnv(name)
		if reason == "" {
			t.Fatalf("%s must be refused", name)
		}
		if !strings.Contains(reason, "gateway-owned") {
			t.Fatalf("the refusal for %s must say the gateway owns the variable: %q", name, reason)
		}
	}
}
