package app

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

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
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.KeyHMACSecret == "" {
		return Config{}, errors.New("API_KEY_HMAC_SECRET is required")
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
