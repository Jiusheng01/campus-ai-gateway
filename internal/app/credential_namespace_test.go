package app

import (
	"strings"
	"testing"
)

// The namespace rule is a prefix rule on purpose: a variable added to a
// gateway-owned family later has to be protected without anyone remembering to
// extend a list of exact names. Every name below belongs to a reserved family
// and appears nowhere in the code, so the day the rules turn into literals this
// fails - which is exactly the property an exact-name list would lose.
func TestCredentialEnvProtectsFamiliesItCannotEnumerate(t *testing.T) {
	if len(reservedCredentialEnvPrefixes) == 0 {
		t.Fatal("no reserved prefixes: the rule would protect nothing")
	}
	for _, prefix := range reservedCredentialEnvPrefixes {
		name := prefix + "A_SETTING_NOBODY_HAS_WRITTEN_YET"
		reason := validateCredentialEnv(name)
		if reason == "" {
			t.Fatalf("%s must be refused: it is in the %s* family", name, prefix)
		}
		if !strings.Contains(reason, prefix) {
			t.Fatalf("the refusal for %s must name the prefix it hit, so the operator can move it: %q", name, reason)
		}
	}
}

// Each family the ticket names needs a real member refused, not just a prefix
// that nothing uses. This is the list an operator is likely to type.
func TestCredentialEnvRefusesEveryFamilyTheTicketNames(t *testing.T) {
	families := []struct{ family, name string }{
		{"management plane", "ADMIN_SESSION_SECRET"},
		{"management plane proxy", "ADMIN_PROXY_SECRET"},
		{"API key signing", "API_KEY_HMAC_SECRET"},
		{"upstream core", "TWCORE_ENDPOINT"},
		{"database", "DATABASE_URL"},
		{"cache", "REDIS_PASSWORD"},
		{"run mode", "DATA_PLANE"},
		{"trusted proxy", "TRUSTED_PROXY_CIDRS"},
	}
	for _, tc := range families {
		t.Run(tc.family, func(t *testing.T) {
			if reason := validateCredentialEnv(tc.name); reason == "" {
				t.Fatalf("%s (%s) must be refused", tc.name, tc.family)
			}
		})
	}
}

// A third-party credential is what the field is for, and the rule must not have
// been tightened into refusing everything that looks like a variable name.
func TestCredentialEnvStillAcceptsThirdPartyCredentials(t *testing.T) {
	for _, name := range []string{
		"DASHSCOPE_API_KEY",
		"DEEPSEEK_API_KEY",
		"ARK_API_KEY",
		"MOONSHOT_API_KEY",
		"SILICONFLOW_API_KEY",
		"ANTHROPIC_API_KEY",
		"OPENAI_API_KEY",
		"MY_OWN_UPSTREAM_TOKEN",
	} {
		if reason := validateCredentialEnv(name); reason != "" {
			t.Fatalf("%s must be accepted, got %q", name, reason)
		}
	}
}

// Two different mistakes get two different answers. "that is not a variable
// name" and "that is a variable the gateway owns" send an operator to two
// different fixes, so collapsing them into one message would cost the operator
// the one thing the rejection exists to give them.
func TestCredentialEnvExplainsWhichRuleWasBroken(t *testing.T) {
	shape := validateCredentialEnv("sk-live-abc123")
	namespace := validateCredentialEnv("ADMIN_SESSION_SECRET")

	if shape == "" || namespace == "" {
		t.Fatalf("both inputs must be refused, got shape=%q namespace=%q", shape, namespace)
	}
	if shape == namespace {
		t.Fatal("a pasted key and a gateway-owned name are different mistakes and need different messages")
	}
	if !strings.Contains(namespace, "gateway-owned") {
		t.Fatalf("the namespace refusal must say the gateway owns the variable: %q", namespace)
	}
	if strings.Contains(shape, "gateway-owned") {
		t.Fatalf("a value that is not a variable name must be refused for its shape, not for its namespace: %q", shape)
	}
}

// The field holds a variable NAME. Anything that is plainly a secret value has
// to be refused, or the indirection the field exists for - keeping the secret
// out of the database - is defeated by pasting it in.
func TestCredentialEnvRefusesValuesThatAreNotNames(t *testing.T) {
	for _, name := range []string{
		"sk-live-abc123",
		"dashscope_api_key",
		"DashScope-Api-Key",
		"",
		"123_STARTS_WITH_A_DIGIT",
	} {
		if reason := validateCredentialEnv(name); reason == "" {
			t.Fatalf("%q is not an upper-case environment variable name and must be refused", name)
		}
	}
}
