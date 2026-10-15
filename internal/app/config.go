package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// AdminSessionSecretMinBytes is the minimum length accepted for an explicitly
// configured ADMIN_SESSION_SECRET. Anything shorter is rejected at startup
// rather than silently weakening access-token signing.
const AdminSessionSecretMinBytes = 32

// adminSessionDerivationLabel domain-separates the development fallback key.
// It guarantees the derived value differs from API_KEY_HMAC_SECRET and from
// every other HMAC the gateway computes with that secret.
const adminSessionDerivationLabel = "campus-admin-session-v1"

// adminRefreshReplayGraceDefault is how long a just-superseded refresh token
// remains acceptable as a benign concurrent refresh when
// ADMIN_REFRESH_REPLAY_GRACE is not set.
//
// Five seconds, not thirty. The window buys exactly one thing: two refreshes
// that genuinely race — two tabs, two requests in flight, a retried refresh —
// converge on the same successor instead of one of them being mistaken for a
// stolen token. Every additional second only extends how long a *stolen
// superseded* token keeps being accepted, which is not a trade worth making.
//
// What this value does not do, stated plainly so no one has to guess: it does
// not protect a stolen *current* refresh token, which is usable regardless of
// this setting, and it does not replace HttpOnly, Secure, SameSite,
// rotation, replay detection or session-family revocation — those remain the
// controls. Shortening the window reduces the previous-generation silent reuse
// window; it does not eliminate refresh-token theft risk.
//
// A deployment behind an unusually slow proxy may raise the value
// deliberately; zero would turn ordinary races into account-wide lockouts.
const adminRefreshReplayGraceDefault = 5 * time.Second

type Config struct {
	HTTPAddr             string
	DatabaseURL          string
	RedisAddr            string
	RedisPassword        string
	RedisDB              int
	KeyHMACSecret        string
	ThinkWatchURL        string
	ThinkWatchKey        string
	ThinkWatchConfigPath string
	ThinkWatchExe        string

	// AdminToken is the break-glass credential. It only takes effect when
	// ADMIN_AUTH_MODE is "token"; in "session" mode it is ignored so a stale
	// environment variable cannot silently bypass the login system.
	AdminToken       string
	AdminAuthMode    string
	AdminProxySecret string
	AdminUserHeader  string
	AdminRolesHeader string

	// Admin session authentication.
	AdminAccessTTL         time.Duration // lifetime of an access token
	AdminRefreshTTL        time.Duration // lifetime of a session and its refresh cookie
	AdminLoginMaxAttempts  int64         // failed attempts allowed per window
	AdminLoginWindow       time.Duration // length of the login rate-limit window
	AdminCookieSecure      bool          // add Secure to the refresh cookie (needs HTTPS)
	AdminSetupLocalOnly    bool          // first-run setup is localhost-only
	TrustedProxyCIDRs      []string      // proxies whose X-Forwarded-For we trust
	AdminBootstrapUsername string        // optional non-interactive bootstrap
	AdminBootstrapPassword string

	// AdminSessionSecret is the dedicated key for administrator session
	// cryptography (access-token HS256 signing and refresh rotation). Keeping
	// it separate from API_KEY_HMAC_SECRET stops a compromise of one security
	// domain from reaching the other. When empty, a domain-separated key is
	// derived from API_KEY_HMAC_SECRET so local development needs no extra
	// configuration; production is expected to set it explicitly.
	AdminSessionSecret string

	// AdminRefreshReplayGrace is how long a just-rotated refresh token is still
	// accepted as a benign concurrent refresh (two browser tabs racing). Reuse
	// beyond this window is treated as replay and revokes the session family.
	// The default lives in adminRefreshReplayGraceDefault; the environment
	// variable ADMIN_REFRESH_REPLAY_GRACE still overrides it.
	AdminRefreshReplayGrace time.Duration

	// PublicRegistrationEnabled opens POST /auth/register to anyone who can
	// reach the login screen.
	//
	// It defaults to false. The confirmed architecture puts campus SSO in
	// front of the gateway, and a "member" has no data-plane capability yet,
	// so an open anonymous password signup adds abuse surface without adding
	// anything an account can do. An operator who wants it — for a controlled
	// trial, or because the deployment genuinely wants local accounts — turns
	// it on explicitly; the rest of the account system is unaffected either
	// way.
	PublicRegistrationEnabled bool
	// PublicRegisterMaxAttempts and PublicRegisterWindow bound how many
	// registration attempts one source address may make. Every attempt counts,
	// including ones that fail on a duplicate username, because the counter
	// exists to stop automated account creation rather than to punish typos.
	PublicRegisterMaxAttempts int64
	PublicRegisterWindow      time.Duration
}

func LoadConfig() (Config, error) {
	cfg := Config{
		HTTPAddr:             getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		RedisAddr:            getenv("REDIS_ADDR", "redis:6379"),
		RedisPassword:        os.Getenv("REDIS_PASSWORD"),
		RedisDB:              int(getenvInt64("REDIS_DB", 0)),
		KeyHMACSecret:        os.Getenv("API_KEY_HMAC_SECRET"),
		ThinkWatchURL:        getenv("THINKWATCH_URL", "http://127.0.0.1:8788"),
		ThinkWatchKey:        os.Getenv("THINKWATCH_GATEWAY_KEY"),
		ThinkWatchConfigPath: getenv("THINKWATCH_CONFIG_PATH", defaultThinkWatchConfigPath()),
		ThinkWatchExe:        os.Getenv("TWCORE_EXE"),

		AdminToken:       os.Getenv("ADMIN_TOKEN"),
		AdminAuthMode:    getenv("ADMIN_AUTH_MODE", "session"),
		AdminProxySecret: os.Getenv("ADMIN_PROXY_SECRET"),
		AdminUserHeader:  getenv("ADMIN_USER_HEADER", "X-Forwarded-User"),
		AdminRolesHeader: getenv("ADMIN_ROLES_HEADER", "X-Forwarded-Groups"),

		AdminAccessTTL:         getenvDuration("ADMIN_ACCESS_TTL", 15*time.Minute),
		AdminRefreshTTL:        getenvDuration("ADMIN_REFRESH_TTL", 30*24*time.Hour),
		AdminLoginMaxAttempts:  getenvInt64("ADMIN_LOGIN_MAX_ATTEMPTS", 8),
		AdminLoginWindow:       getenvDuration("ADMIN_LOGIN_WINDOW", 15*time.Minute),
		AdminCookieSecure:      getenvBool("ADMIN_COOKIE_SECURE", false),
		AdminSetupLocalOnly:    getenvBool("ADMIN_SETUP_LOCAL_ONLY", true),
		TrustedProxyCIDRs:      splitCSV(os.Getenv("TRUSTED_PROXY_CIDRS")),
		AdminBootstrapUsername: strings.TrimSpace(os.Getenv("ADMIN_BOOTSTRAP_USERNAME")),
		AdminBootstrapPassword: os.Getenv("ADMIN_BOOTSTRAP_PASSWORD"),

		AdminSessionSecret:      os.Getenv("ADMIN_SESSION_SECRET"),
		AdminRefreshReplayGrace: getenvDuration("ADMIN_REFRESH_REPLAY_GRACE", adminRefreshReplayGraceDefault),

		PublicRegistrationEnabled: getenvBool("PUBLIC_REGISTRATION_ENABLED", false),
		PublicRegisterMaxAttempts: getenvInt64("PUBLIC_REGISTER_MAX_ATTEMPTS", 5),
		PublicRegisterWindow:      getenvDuration("PUBLIC_REGISTER_WINDOW", time.Hour),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.KeyHMACSecret == "" {
		return Config{}, errors.New("API_KEY_HMAC_SECRET is required")
	}
	if s := strings.TrimSpace(cfg.AdminSessionSecret); s != "" && len(s) < AdminSessionSecretMinBytes {
		return Config{}, errors.New("ADMIN_SESSION_SECRET must be at least 32 bytes of entropy")
	}
	if cfg.AdminRefreshReplayGrace <= 0 {
		return Config{}, errors.New("ADMIN_REFRESH_REPLAY_GRACE must be positive")
	}

	switch strings.ToLower(strings.TrimSpace(cfg.AdminAuthMode)) {
	case "session":
		if cfg.AdminAccessTTL <= 0 {
			return Config{}, errors.New("ADMIN_ACCESS_TTL must be positive in session mode")
		}
		if cfg.AdminRefreshTTL <= 0 {
			return Config{}, errors.New("ADMIN_REFRESH_TTL must be positive in session mode")
		}
		if cfg.AdminRefreshTTL <= cfg.AdminAccessTTL {
			return Config{}, errors.New("ADMIN_REFRESH_TTL must be longer than ADMIN_ACCESS_TTL")
		}
	}
	if cfg.AdminLoginMaxAttempts < 0 {
		return Config{}, errors.New("ADMIN_LOGIN_MAX_ATTEMPTS cannot be negative")
	}
	if cfg.AdminLoginWindow <= 0 {
		return Config{}, errors.New("ADMIN_LOGIN_WINDOW must be positive")
	}
	if cfg.PublicRegisterMaxAttempts < 0 {
		return Config{}, errors.New("PUBLIC_REGISTER_MAX_ATTEMPTS cannot be negative")
	}
	if cfg.PublicRegisterWindow <= 0 {
		return Config{}, errors.New("PUBLIC_REGISTER_WINDOW must be positive")
	}
	for _, cidr := range cfg.TrustedProxyCIDRs {
		if _, _, err := parseCIDROrIP(cidr); err != nil {
			return Config{}, errors.New("TRUSTED_PROXY_CIDRS contains an invalid entry: " + cidr)
		}
	}
	return cfg, nil
}

// parseCIDROrIP accepts either a CIDR block or a bare address. A bare address is
// widened to a single-host prefix so the trusted-proxy check can treat both
// forms uniformly.
func parseCIDROrIP(raw string) (*net.IPNet, net.IP, error) {
	if strings.Contains(raw, "/") {
		ip, ipNet, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, nil, err
		}
		return ipNet, ip, nil
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		return nil, nil, errors.New("not an IP address or CIDR block")
	}
	bits := 128
	if ip.To4() != nil {
		bits = 32
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, ip, nil
}

// SessionAuthEnabled reports whether the account/password login system is the
// active authentication mode. The "token" and "trusted-header" modes are kept
// for existing deployments and are treated as opt-in legacy paths.
func (c Config) SessionAuthEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(c.AdminAuthMode), "session")
}

// PublicRegistrationOpen reports whether local account registration is served
// at all.
//
// Registration belongs to the local account system, so the two other modes
// never offer it: trusted-header deployments get their accounts from the campus
// identity platform, and the token mode is a break-glass path with no accounts
// behind it.
func (c Config) PublicRegistrationOpen() bool {
	return c.SessionAuthEnabled() && c.PublicRegistrationEnabled
}

// AdminSessionKey returns the key material for administrator session
// cryptography: access-token HS256 signing and refresh-token rotation.
//
// The returned bool reports whether the development fallback was used, so the
// caller can emit a startup warning. The fallback is a labelled HMAC over
// API_KEY_HMAC_SECRET, which keeps the admin-session purpose domain-separated
// from the API-key purpose instead of reusing the raw secret.
func (c Config) AdminSessionKey() ([]byte, bool) {
	if s := strings.TrimSpace(c.AdminSessionSecret); s != "" {
		return []byte(s), false
	}
	mac := hmac.New(sha256.New, []byte(c.KeyHMACSecret))
	mac.Write([]byte(adminSessionDerivationLabel))
	return mac.Sum(nil), true
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt64(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func getenvBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func defaultThinkWatchConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return dir + string(os.PathSeparator) + "ThinkWatch" + string(os.PathSeparator) + "config.yaml"
}
