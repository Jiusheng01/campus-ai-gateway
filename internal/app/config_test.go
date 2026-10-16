package app

import (
	"testing"
	"time"
)

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
