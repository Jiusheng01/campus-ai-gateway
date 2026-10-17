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

// --- data plane selection --------------------------------------------------

// loadConfigWithDataPlane exercises the real environment-parsing path for the
// two variables that decide which data plane the gateway runs.
func loadConfigWithDataPlane(t *testing.T, dataPlane, thinkWatchURL string) (Config, error) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://campus:campus@127.0.0.1:5432/phase1_config_test?sslmode=disable")
	t.Setenv("API_KEY_HMAC_SECRET", "phase1-config-test-secret")
	t.Setenv("ADMIN_SESSION_SECRET", "")
	t.Setenv("ADMIN_AUTH_MODE", "session")
	t.Setenv("DATA_PLANE", dataPlane)
	t.Setenv("THINKWATCH_URL", thinkWatchURL)
	return LoadConfig()
}

// The default is a rule, not a value: configuring THINKWATCH_URL keeps routing
// through it, and not configuring it gets the in-process plane. Every entry
// point in this repository sets THINKWATCH_URL, so existing deployments are
// unchanged; a bare binary stops needing a second process.
func TestDataPlaneAutoFollowsThinkWatchURL(t *testing.T) {
	cases := []struct {
		name      string
		dataPlane string
		url       string
		want      string
		wantThink bool
	}{
		{name: "nothing configured means direct", dataPlane: "", url: "", want: DataPlaneDirect},
		{name: "a configured URL means ThinkWatch", dataPlane: "", url: "http://127.0.0.1:8788", want: DataPlaneThinkWatch, wantThink: true},
		{name: "auto is spelled out", dataPlane: "auto", url: "http://127.0.0.1:8788", want: DataPlaneThinkWatch, wantThink: true},
		{name: "auto without a URL means direct", dataPlane: "AUTO", url: "  ", want: DataPlaneDirect},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadConfigWithDataPlane(t, tc.dataPlane, tc.url)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.DataPlane != tc.want {
				t.Fatalf("DataPlane = %q, want %q", cfg.DataPlane, tc.want)
			}
			if cfg.UsesThinkWatch() != tc.wantThink {
				t.Fatalf("UsesThinkWatch() = %v, want %v", cfg.UsesThinkWatch(), tc.wantThink)
			}
			if cfg.DataPlaneMode() != tc.want {
				t.Fatalf("DataPlaneMode() = %q, want %q", cfg.DataPlaneMode(), tc.want)
			}
		})
	}
}

// Naming the plane you want must win over the presence of a URL, otherwise an
// operator could not opt out of ThinkWatch without deleting configuration other
// tooling still reads.
func TestDataPlaneExplicitDirectWinsOverAConfiguredURL(t *testing.T) {
	cfg, err := loadConfigWithDataPlane(t, "direct", "http://127.0.0.1:8788")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.UsesThinkWatch() {
		t.Fatalf("DATA_PLANE=direct must be honoured even when THINKWATCH_URL is set")
	}
}

func TestDataPlaneThinkWatchRequiresAURL(t *testing.T) {
	if _, err := loadConfigWithDataPlane(t, "thinkwatch", ""); err == nil {
		t.Fatal("DATA_PLANE=thinkwatch without THINKWATCH_URL must fail startup rather than fall back")
	}
}

func TestDataPlaneRejectsAnUnknownValue(t *testing.T) {
	if _, err := loadConfigWithDataPlane(t, "thinkwatch-direct", "http://127.0.0.1:8788"); err == nil {
		t.Fatal("an unknown DATA_PLANE must be rejected")
	}
}

// url.Parse accepts "127.0.0.1:8788" and calls "127.0.0.1" the scheme, which
// would surface as a dial error on the first business request instead of a
// startup failure.
func TestDataPlaneRejectsAThinkWatchURLWithoutAScheme(t *testing.T) {
	if _, err := loadConfigWithDataPlane(t, "thinkwatch", "127.0.0.1:8788"); err == nil {
		t.Fatal("a THINKWATCH_URL without an http(s) scheme must be rejected")
	}
}

// A Config built as a literal — in a test, or by a caller that predates this
// field — must keep the old behaviour. Opting out of a routing process has to
// be deliberate, never the consequence of a field nobody set.
func TestZeroValueConfigKeepsThinkWatch(t *testing.T) {
	var cfg Config
	if !cfg.UsesThinkWatch() {
		t.Fatal("a zero-value Config must not switch the deployment onto the direct plane")
	}
	if cfg.DataPlaneMode() != DataPlaneThinkWatch {
		t.Fatalf("DataPlaneMode() = %q, want %q", cfg.DataPlaneMode(), DataPlaneThinkWatch)
	}
}
