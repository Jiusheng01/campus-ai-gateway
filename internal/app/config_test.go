package app

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// Credential sealing follows the AdminSessionKey pattern: an explicitly
// configured CREDENTIAL_ENCRYPTION_KEY is used as-is (hashed to AES-256's 32
// bytes), and when it is absent a domain-separated key is derived from
// API_KEY_HMAC_SECRET so local development needs no extra variable. The second
// return value is what drives the startup warning.
func TestCredentialSealingKeyConfiguredAndDerived(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://campus:campus@127.0.0.1:5432/phase1_config_test?sslmode=disable")
	t.Setenv("API_KEY_HMAC_SECRET", "phase1-config-test-secret")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "")

	derivedCfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	derivedKey, derived := derivedCfg.CredentialSealingKey()
	if !derived {
		t.Fatal("an absent CREDENTIAL_ENCRYPTION_KEY must report the derived fallback so startup can warn")
	}
	if len(derivedKey) != 32 {
		t.Fatalf("derived key is %d bytes, want 32 for AES-256", len(derivedKey))
	}

	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "an-explicitly-configured-operator-secret")
	configuredCfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	configuredKey, configuredDerived := configuredCfg.CredentialSealingKey()
	if configuredDerived {
		t.Fatal("a configured CREDENTIAL_ENCRYPTION_KEY must not report the fallback")
	}
	if len(configuredKey) != 32 {
		t.Fatalf("configured key is %d bytes, want 32 for AES-256", len(configuredKey))
	}
	if bytes.Equal(derivedKey, configuredKey) {
		t.Fatal("the configured key must not equal the derived fallback")
	}
}

// loadConfigWithRegistration writes the minimum environment LoadConfig needs and
// then the value under test, so the assertions run against the real
// environment-parsing path rather than a hand-built Config.
func loadConfigWithRegistration(t *testing.T, value string) Config {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://campus:campus@127.0.0.1:5432/phase1_config_test?sslmode=disable")
	t.Setenv("API_KEY_HMAC_SECRET", "phase1-config-test-secret")
	t.Setenv("ADMIN_SESSION_SECRET", "")
	t.Setenv("ADMIN_AUTH_MODE", "session")
	t.Setenv("PUBLIC_REGISTRATION_ENABLED", value)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// P1-03. A deployment that does not set the switch must not offer password
// registration.
//
// The confirmed architecture puts campus SSO in front of the gateway, and a
// member has no data-plane capability yet, so an open anonymous signup adds
// abuse surface without adding anything an account could do. The old default of
// true meant every new deployment opened registration the moment it booted.
// This test fails on the old default (true) and passes on the new one (false).
func TestPublicRegistrationDefaultsToOff(t *testing.T) {
	cfg := loadConfigWithRegistration(t, "")
	if cfg.PublicRegistrationEnabled {
		t.Fatal("an unset PUBLIC_REGISTRATION_ENABLED must default to false")
	}
	if cfg.PublicRegistrationOpen() {
		t.Fatal("registration must not be open by default")
	}
}

// An operator who wants local accounts is not punished for it: every accepted
// spelling of "on" and "off" keeps working, and an unrecognised value falls
// back to the default rather than guessing.
func TestPublicRegistrationExplicitValues(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"1", true},
		{"yes", true},
		{"on", true},
		{"TRUE", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"off", false},
		{"False", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			cfg := loadConfigWithRegistration(t, tc.value)
			if cfg.PublicRegistrationEnabled != tc.want {
				t.Fatalf("PUBLIC_REGISTRATION_ENABLED=%q -> %v, want %v", tc.value, cfg.PublicRegistrationEnabled, tc.want)
			}
			if cfg.PublicRegistrationOpen() != tc.want {
				t.Fatalf("PublicRegistrationOpen() = %v, want %v", cfg.PublicRegistrationOpen(), tc.want)
			}
		})
	}
}

// The switch only decides whether registration is offered at all; the mode
// decides whether the endpoint exists. Even switched on, the token and
// trusted-header modes must keep answering 404, because those deployments have
// no local accounts to register.
func TestPublicRegistrationRequiresSessionMode(t *testing.T) {
	cfg := loadConfigWithRegistration(t, "true")
	for _, mode := range []string{"token", "trusted-header"} {
		cfg.AdminAuthMode = mode
		if cfg.PublicRegistrationOpen() {
			t.Fatalf("%s mode must not offer registration even when the switch is on", mode)
		}
	}
}

// loadConfigWithGrace writes the minimum environment LoadConfig needs and then
// the ADMIN_REFRESH_REPLAY_GRACE value under test. An empty value models the
// variable being unset.
func loadConfigWithGrace(t *testing.T, raw string) Config {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://campus:campus@127.0.0.1:5432/phase1_config_test?sslmode=disable")
	t.Setenv("API_KEY_HMAC_SECRET", "phase1-config-test-secret")
	t.Setenv("ADMIN_SESSION_SECRET", "")
	t.Setenv("ADMIN_AUTH_MODE", "session")
	t.Setenv("ADMIN_REFRESH_REPLAY_GRACE", raw)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// P1-04 (confirmed). The benign-concurrent-refresh window defaults to five
// seconds, and an explicit environment value still wins. The window is what
// makes two genuinely racing refreshes converge on the same successor; it does
// not protect a stolen current token and does not replace HttpOnly, Secure,
// SameSite, rotation, replay detection or family revocation.
func TestRefreshReplayGraceDefaultAndOverride(t *testing.T) {
	unset := loadConfigWithGrace(t, "")
	if unset.AdminRefreshReplayGrace != 5*time.Second {
		t.Fatalf("an unset ADMIN_REFRESH_REPLAY_GRACE must default to 5s, got %s", unset.AdminRefreshReplayGrace)
	}
	if unset.AdminRefreshReplayGrace != adminRefreshReplayGraceDefault {
		t.Fatalf("the default must be the named constant, got %s", unset.AdminRefreshReplayGrace)
	}

	for _, raw := range []string{"10s", "1m", "30s"} {
		cfg := loadConfigWithGrace(t, raw)
		want, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AdminRefreshReplayGrace != want {
			t.Fatalf("ADMIN_REFRESH_REPLAY_GRACE=%q -> %s, want %s", raw, cfg.AdminRefreshReplayGrace, want)
		}
	}

	// A zero, negative or unparseable value falls back to the safe default
	// rather than a zero window that would turn ordinary races into
	// account-wide lockouts. This is the project's existing duration semantics,
	// and it fails toward safety.
	for _, raw := range []string{"0", "0s", "-1s", "nonsense"} {
		cfg := loadConfigWithGrace(t, raw)
		if cfg.AdminRefreshReplayGrace != adminRefreshReplayGraceDefault {
			t.Fatalf("ADMIN_REFRESH_REPLAY_GRACE=%q -> %s, want the %s default", raw, cfg.AdminRefreshReplayGrace, adminRefreshReplayGraceDefault)
		}
	}
}

// loadConfigWithPublicOrigin runs the real environment-parsing path with
// ADMIN_PUBLIC_ORIGIN set to the given value.
func loadConfigWithPublicOrigin(t *testing.T, value string) (Config, error) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://campus:campus@127.0.0.1:5432/phase1_config_test?sslmode=disable")
	t.Setenv("API_KEY_HMAC_SECRET", "phase1-config-test-secret")
	t.Setenv("ADMIN_SESSION_SECRET", "")
	t.Setenv("ADMIN_AUTH_MODE", "session")
	t.Setenv("ADMIN_PUBLIC_ORIGIN", value)
	return LoadConfig()
}

// ADMIN_PUBLIC_ORIGIN is reduced to the scheme://authority the same-origin check
// compares against. An operator pasting the console URL is doing the obvious
// thing, so the path is dropped rather than treated as an error; a value that
// is not an http(s) origin is a real mistake and must fail startup instead of
// quietly disconnecting the CSRF check.
func TestAdminPublicOriginNormalisation(t *testing.T) {
	cfg, err := loadConfigWithPublicOrigin(t, "https://gateway.example.edu/admin-ui/")
	if err != nil {
		t.Fatalf("a console URL must be accepted: %v", err)
	}
	if cfg.AdminPublicOrigin != "https://gateway.example.edu" {
		t.Fatalf("the path must be dropped, got %q", cfg.AdminPublicOrigin)
	}

	cfg, err = loadConfigWithPublicOrigin(t, "http://gateway.example.edu:8088")
	if err != nil {
		t.Fatalf("a non-default port must be accepted: %v", err)
	}
	if cfg.AdminPublicOrigin != "http://gateway.example.edu:8088" {
		t.Fatalf("the port must be preserved, got %q", cfg.AdminPublicOrigin)
	}
}

func TestAdminPublicOriginRejectsWhatItCannotUse(t *testing.T) {
	for _, raw := range []string{"gateway.example.edu", "ftp://gateway.example.edu", "https://"} {
		if _, err := loadConfigWithPublicOrigin(t, raw); err == nil {
			t.Fatalf("ADMIN_PUBLIC_ORIGIN=%q must be refused", raw)
		}
	}
}

// Unset is the common case and must stay a no-op: the check then rests on the
// request's own authority.
func TestAdminPublicOriginDefaultsToUnset(t *testing.T) {
	cfg, err := loadConfigWithPublicOrigin(t, "")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AdminPublicOrigin != "" {
		t.Fatalf("an unset ADMIN_PUBLIC_ORIGIN must stay empty, got %q", cfg.AdminPublicOrigin)
	}
}

// --- data plane selection --------------------------------------------------

// loadConfigWithDataPlane exercises the real environment-parsing path for the
// variable that decides which data plane the gateway runs.
func loadConfigWithDataPlane(t *testing.T, dataPlane string) (Config, error) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://campus:campus@127.0.0.1:5432/phase1_config_test?sslmode=disable")
	t.Setenv("API_KEY_HMAC_SECRET", "phase1-config-test-secret")
	t.Setenv("ADMIN_SESSION_SECRET", "")
	t.Setenv("ADMIN_AUTH_MODE", "session")
	t.Setenv("DATA_PLANE", dataPlane)
	return LoadConfig()
}

// The direct plane is the only data plane, so every accepted spelling of "the
// default" resolves to it and nothing else has to be configured.
func TestDataPlaneResolvesToDirect(t *testing.T) {
	for _, tc := range []struct{ name, dataPlane string }{
		{name: "nothing configured", dataPlane: ""},
		{name: "auto", dataPlane: "auto"},
		{name: "auto is case-insensitive", dataPlane: "AUTO"},
		{name: "direct", dataPlane: "direct"},
		{name: "direct with surrounding whitespace", dataPlane: "  direct  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadConfigWithDataPlane(t, tc.dataPlane)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.DataPlane != DataPlaneDirect {
				t.Fatalf("DataPlane = %q, want %q", cfg.DataPlane, DataPlaneDirect)
			}
			if got := cfg.DataPlaneMode(); got != DataPlaneDirect {
				t.Fatalf("DataPlaneMode() = %q, want %q", got, DataPlaneDirect)
			}
		})
	}
}

// The modes that used to select a second process are gone, and naming one has
// to stop startup rather than resolve to something else. An operator whose
// environment still names a retired mode would otherwise get a gateway that
// behaves differently from what the variable claims, and would have no way to
// notice until a request went somewhere unexpected.
func TestDataPlaneRejectsRetiredModes(t *testing.T) {
	for _, mode := range []string{"think" + "watch", "shadow", "THINK" + "WATCH", "Shadow"} {
		t.Run("DATA_PLANE="+mode, func(t *testing.T) {
			if _, err := loadConfigWithDataPlane(t, mode); err == nil {
				t.Fatal("a removed data plane must fail startup rather than fall back")
			}
		})
	}
}

func TestDataPlaneRejectsAnUnknownValue(t *testing.T) {
	if _, err := loadConfigWithDataPlane(t, "bogus"); err == nil {
		t.Fatal("an unknown DATA_PLANE must be rejected")
	}
}

// A Config built as a literal — in a test, or by a caller that predates this
// field — must keep the behaviour this gateway actually has: there is one data
// plane, and a zero value reads as it.
func TestZeroValueConfigIsTheDirectPlane(t *testing.T) {
	var cfg Config
	if got := cfg.DataPlaneMode(); got != DataPlaneDirect {
		t.Fatalf("DataPlaneMode() = %q, want %q", got, DataPlaneDirect)
	}
}

// loadConfigWithProviderHTTPAllowlist runs the real environment-parsing path
// for the plaintext-provider exception list.
func loadConfigWithProviderHTTPAllowlist(t *testing.T, value string) (Config, error) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://campus:campus@127.0.0.1:5432/phase1_config_test?sslmode=disable")
	t.Setenv("API_KEY_HMAC_SECRET", "phase1-config-test-secret")
	t.Setenv("ADMIN_SESSION_SECRET", "")
	t.Setenv("ADMIN_AUTH_MODE", "session")
	t.Setenv("PROVIDER_HTTP_ALLOWLIST", value)
	return LoadConfig()
}

// PROVIDER_HTTP_ALLOWLIST is a comma-separated host/CIDR list. Unset means
// "no plaintext targets at all", and a malformed entry must stop startup — a
// typo that silently matched nothing would look exactly like a deliberate
// deny, and the operator would not find out until a campus channel failed.
func TestProviderHTTPAllowlistConfig(t *testing.T) {
	cfg, err := loadConfigWithProviderHTTPAllowlist(t, "127.0.0.1, 10.0.0.0/8, campus-proxy.example.edu")
	if err != nil {
		t.Fatalf("valid entries must be accepted: %v", err)
	}
	if len(cfg.ProviderHTTPAllowlist) != 3 {
		t.Fatalf("want three entries, got %#v", cfg.ProviderHTTPAllowlist)
	}

	empty, err := loadConfigWithProviderHTTPAllowlist(t, "")
	if err != nil {
		t.Fatalf("an unset allowlist must be accepted: %v", err)
	}
	if len(empty.ProviderHTTPAllowlist) != 0 {
		t.Fatalf("an unset allowlist must stay empty, got %#v", empty.ProviderHTTPAllowlist)
	}

	for _, bad := range []string{
		"http://campus-proxy.example.edu",
		"10.0.0.0/33",
		"bad host",
		"-campus.example.edu",
		"campus..example.edu",
	} {
		if _, err := loadConfigWithProviderHTTPAllowlist(t, bad); err == nil {
			t.Fatalf("PROVIDER_HTTP_ALLOWLIST=%q must fail startup", bad)
		}
	}
}

// loadConfigWithSecrets applies the environment LoadConfig needs to reach the
// secret checks, then the values under test. Every secret the checks read is
// written explicitly so a value left over from the surrounding process cannot
// decide the outcome.
func loadConfigWithSecrets(t *testing.T, overrides map[string]string) (Config, error) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://campus:campus@127.0.0.1:5432/phase1_config_test?sslmode=disable")
	t.Setenv("API_KEY_HMAC_SECRET", "phase1-config-test-secret")
	t.Setenv("ADMIN_SESSION_SECRET", "")
	t.Setenv("ADMIN_AUTH_MODE", "session")
	t.Setenv("ADMIN_TOKEN", "")
	t.Setenv("ADMIN_PROXY_SECRET", "")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "")
	t.Setenv("DATA_PLANE", "direct")
	for key, value := range overrides {
		t.Setenv(key, value)
	}
	return LoadConfig()
}

// A placeholder is not a weak configuration, it is a startup trap: the gateway
// accepts it now, and the day an operator replaces it every stored API-key
// digest stops matching. Refusing to start is the only outcome that cannot be
// missed, and the message has to name the variable, say what is wrong and say
// how to produce a usable value.
func TestAPIKeyHMACSecretRejectsKnownPlaceholders(t *testing.T) {
	for _, placeholder := range []string{
		"change-me-in-production",
		"replace-with-a-long-random-secret",
		"changeme",
		"CHANGEME",
	} {
		t.Run(placeholder, func(t *testing.T) {
			_, err := loadConfigWithSecrets(t, map[string]string{"API_KEY_HMAC_SECRET": placeholder})
			if err == nil {
				t.Fatalf("API_KEY_HMAC_SECRET=%q must stop startup", placeholder)
			}
			if !strings.Contains(err.Error(), "placeholder") {
				t.Fatalf("the refusal must say the value is a placeholder, got %q", err.Error())
			}
			if !strings.Contains(err.Error(), "API_KEY_HMAC_SECRET") {
				t.Fatalf("the refusal must name the variable, got %q", err.Error())
			}
			if !strings.Contains(err.Error(), "openssl rand") {
				t.Fatalf("the refusal must say how to produce a usable secret, got %q", err.Error())
			}
		})
	}
}

// The HMAC secret is the only input behind every stored API-key digest, so a
// short one can be brute-forced offline against a stolen digest. Sixteen bytes
// is the floor; the local development default from scripts/dev.ps1 is above it
// and must keep starting without extra configuration.
func TestAPIKeyHMACSecretLengthFloor(t *testing.T) {
	for _, short := range []string{"123456789012345", "a"} {
		t.Run("rejects "+short, func(t *testing.T) {
			_, err := loadConfigWithSecrets(t, map[string]string{"API_KEY_HMAC_SECRET": short})
			if err == nil {
				t.Fatalf("a %d-byte API_KEY_HMAC_SECRET must stop startup", len(short))
			}
			if !strings.Contains(err.Error(), "at least 16 bytes") {
				t.Fatalf("the refusal must state the floor, got %q", err.Error())
			}
			if !strings.Contains(err.Error(), "openssl rand") {
				t.Fatalf("the refusal must say how to produce a usable secret, got %q", err.Error())
			}
		})
	}

	if _, err := loadConfigWithSecrets(t, map[string]string{"API_KEY_HMAC_SECRET": strings.Repeat("a", 16)}); err != nil {
		t.Fatalf("a 16-byte secret must be accepted: %v", err)
	}

	// The exact default scripts/dev.ps1 passes when API_KEY_HMAC_SECRET is not
	// already set. If it ever drops below the floor, local development starts
	// failing on a value the repository itself chose.
	const devDefault = "campus-local-test-secret"
	if len(devDefault) < 16 {
		t.Fatalf("scripts/dev.ps1 default is %d bytes, below the 16-byte floor", len(devDefault))
	}
	cfg, err := loadConfigWithSecrets(t, map[string]string{"API_KEY_HMAC_SECRET": devDefault})
	if err != nil {
		t.Fatalf("the scripts/dev.ps1 default must be accepted: %v", err)
	}
	if cfg.KeyHMACSecret != devDefault {
		t.Fatalf("KeyHMACSecret = %q, want the development default", cfg.KeyHMACSecret)
	}
}

// CREDENTIAL_ENCRYPTION_KEY seals inline Provider credentials. A placeholder
// here is the same trap as a placeholder HMAC secret: it works until it is
// replaced, and then every stored credential becomes unreadable. An absent key
// still takes the documented development fallback, so only a set value is
// checked.
func TestCredentialEncryptionKeyRejectsKnownPlaceholders(t *testing.T) {
	for _, placeholder := range []string{
		"change-me-in-production",
		"replace-with-a-long-random-secret",
		"changeme",
	} {
		t.Run(placeholder, func(t *testing.T) {
			_, err := loadConfigWithSecrets(t, map[string]string{"CREDENTIAL_ENCRYPTION_KEY": placeholder})
			if err == nil {
				t.Fatalf("CREDENTIAL_ENCRYPTION_KEY=%q must stop startup", placeholder)
			}
			if !strings.Contains(err.Error(), "CREDENTIAL_ENCRYPTION_KEY") {
				t.Fatalf("the refusal must name the variable, got %q", err.Error())
			}
			if !strings.Contains(err.Error(), "placeholder") {
				t.Fatalf("the refusal must say the value is a placeholder, got %q", err.Error())
			}
			if !strings.Contains(err.Error(), "openssl rand") {
				t.Fatalf("the refusal must say how to produce a usable key, got %q", err.Error())
			}
		})
	}

	if _, err := loadConfigWithSecrets(t, nil); err != nil {
		t.Fatalf("an absent CREDENTIAL_ENCRYPTION_KEY must keep the development fallback: %v", err)
	}
}

// ADMIN_TOKEN is the break-glass credential for token mode. Only token mode
// reads it, so a leftover placeholder in the other modes must not stop startup;
// in token mode it is the one thing between an anonymous caller and the control
// plane, and a shipped example is not a credential.
func TestAdminTokenPlaceholderIsCheckedOnlyInTokenMode(t *testing.T) {
	const placeholder = "changeme"

	cfg, err := loadConfigWithSecrets(t, map[string]string{
		"ADMIN_AUTH_MODE": "session",
		"ADMIN_TOKEN":     placeholder,
	})
	if err != nil {
		t.Fatalf("session mode does not read ADMIN_TOKEN and must start: %v", err)
	}
	if cfg.AdminToken != placeholder {
		t.Fatalf("AdminToken = %q, want the ignored value to be preserved", cfg.AdminToken)
	}

	_, err = loadConfigWithSecrets(t, map[string]string{
		"ADMIN_AUTH_MODE": "token",
		"ADMIN_TOKEN":     placeholder,
	})
	if err == nil {
		t.Fatal("token mode must refuse a placeholder ADMIN_TOKEN")
	}
	if !strings.Contains(err.Error(), "ADMIN_TOKEN") || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("the refusal must name ADMIN_TOKEN and the problem, got %q", err.Error())
	}

	if _, err := loadConfigWithSecrets(t, map[string]string{
		"ADMIN_AUTH_MODE": "token",
		"ADMIN_TOKEN":     "a-long-random-break-glass-token",
	}); err != nil {
		t.Fatalf("token mode must accept a real token: %v", err)
	}
}

// The placeholder check belongs to trusted-header mode, which is the only mode
// that reads ADMIN_PROXY_SECRET. The value used here is longer than the
// 32-character floor, so the refusal can only come from the placeholder rule.
func TestAdminProxySecretPlaceholderIsCheckedOnlyInTrustedHeaderMode(t *testing.T) {
	const placeholder = "replace-with-a-long-random-secret"

	for _, mode := range []string{"session", "token"} {
		t.Run(mode, func(t *testing.T) {
			if _, err := loadConfigWithSecrets(t, map[string]string{
				"ADMIN_AUTH_MODE":    mode,
				"ADMIN_PROXY_SECRET": placeholder,
			}); err != nil {
				t.Fatalf("%s mode does not read ADMIN_PROXY_SECRET and must start: %v", mode, err)
			}
		})
	}

	t.Run("trusted-header", func(t *testing.T) {
		_, err := loadConfigWithSecrets(t, map[string]string{
			"ADMIN_AUTH_MODE":    "trusted-header",
			"ADMIN_PROXY_SECRET": placeholder,
		})
		if err == nil {
			t.Fatal("trusted-header mode must refuse a placeholder ADMIN_PROXY_SECRET")
		}
		if !strings.Contains(err.Error(), "ADMIN_PROXY_SECRET") || !strings.Contains(err.Error(), "placeholder") {
			t.Fatalf("the refusal must name ADMIN_PROXY_SECRET and the problem, got %q", err.Error())
		}
		if !strings.Contains(err.Error(), "openssl rand") {
			t.Fatalf("the refusal must say how to produce a usable secret, got %q", err.Error())
		}
	})
}

// setConfigEnv applies the environment LoadConfig needs to get as far as the
// Admin UI settings. The cases that care about a particular field override it
// after calling this, so the five values that only have to be present are
// stated once.
func setConfigEnv(t *testing.T, adminAuthMode string) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://campus:campus@127.0.0.1:5432/phase1_config_test?sslmode=disable")
	t.Setenv("API_KEY_HMAC_SECRET", "phase1-config-test-secret")
	t.Setenv("ADMIN_SESSION_SECRET", "phase1-session-signing-secret-value")
	t.Setenv("DATA_PLANE", "direct")
	t.Setenv("ADMIN_AUTH_MODE", adminAuthMode)
}

// trusted-header trusts one shared string for the entire control plane, so a
// short one is not a weak setting — it is an open door. Refusing to start is
// the only response that cannot be ignored by accident.
func TestTrustedHeaderRequiresALongProxySecret(t *testing.T) {
	base := func(t *testing.T) {
		setConfigEnv(t, "trusted-header")
	}

	base(t)
	t.Setenv("ADMIN_PROXY_SECRET", "short")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("a short ADMIN_PROXY_SECRET must stop startup in trusted-header mode")
	}

	base(t)
	t.Setenv("ADMIN_PROXY_SECRET", strings.Repeat("s", minAdminProxySecretLen))
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("a full-length secret must be accepted: %v", err)
	}
	if !cfg.AdminAuthModeTrustedHeader() {
		t.Fatal("AdminAuthModeTrustedHeader must agree with the configured mode")
	}
	if len(cfg.AdminProxyCIDRs) != 0 {
		t.Fatalf("an unset ADMIN_PROXY_CIDRS must stay empty, got %#v", cfg.AdminProxyCIDRs)
	}

	base(t)
	t.Setenv("ADMIN_PROXY_SECRET", strings.Repeat("s", minAdminProxySecretLen))
	t.Setenv("ADMIN_PROXY_CIDRS", "10.0.0.0/8, 127.0.0.1")
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("valid CIDRs must be accepted: %v", err)
	}
	if len(cfg.AdminProxyCIDRs) != 2 {
		t.Fatalf("want two parsed entries, got %#v", cfg.AdminProxyCIDRs)
	}

	base(t)
	t.Setenv("ADMIN_PROXY_SECRET", strings.Repeat("s", minAdminProxySecretLen))
	t.Setenv("ADMIN_PROXY_CIDRS", "not-an-address")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("an invalid ADMIN_PROXY_CIDRS entry must be rejected at startup")
	}
}
