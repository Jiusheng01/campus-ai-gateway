package app

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr string
	DatabaseURL string
	RedisAddr string
	RedisPassword string
	RedisDB int
	KeyHMACSecret string
	ThinkWatchURL string
	ThinkWatchKey string
	ThinkWatchConfigPath string
	ThinkWatchExe string
	AdminToken string
	AdminAuthMode string
	AdminProxySecret string
	AdminUserHeader string
	AdminRolesHeader string
	// AdminSessionTTL is how long a password login stays valid. Sessions are
	// held in Redis, so revoking one is a single key deletion.
	AdminSessionTTL time.Duration
	// AdminLoginMaxAttempts caps failed password attempts per source IP inside
	// AdminLoginWindow. Zero disables the throttle.
	AdminLoginMaxAttempts int64
	AdminLoginWindow time.Duration
	// AdminBootstrapUsername/Password seed the first account on startup so a
	// scripted deployment never has to drive the setup wizard.
	AdminBootstrapUsername string
	AdminBootstrapPassword string
	// DefaultMaxOutputTokens is the output budget reserved when a caller does not
	// send max_tokens. Without it a request reserves only its input size, so a
	// 50-token prompt that generates 4000 tokens consumes almost nothing of the
	// caller's TPM allowance.
	DefaultMaxOutputTokens int64
	// StreamIncludeUsage asks upstreams to report token usage for streaming
	// responses. OpenAI-compatible providers omit usage unless it is requested,
	// which would leave tenant quota reconciliation without actual numbers.
	StreamIncludeUsage bool
}

func LoadConfig() (Config, error) {
	cfg := Config{
		HTTPAddr: getenv("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisAddr: getenv("REDIS_ADDR", "redis:6379"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		KeyHMACSecret: os.Getenv("API_KEY_HMAC_SECRET"),
		ThinkWatchURL: getenv("THINKWATCH_URL", "http://127.0.0.1:8788"),
		ThinkWatchKey: os.Getenv("THINKWATCH_GATEWAY_KEY"),
		ThinkWatchConfigPath: getenv("THINKWATCH_CONFIG_PATH", defaultThinkWatchConfigPath()),
		ThinkWatchExe: os.Getenv("TWCORE_EXE"),
		AdminToken: os.Getenv("ADMIN_TOKEN"),
		// session is the default because a shared bearer token cannot be
		// attributed to a person, cannot be revoked, and has to be copied
		// around by hand. token/trusted-header remain available.
		AdminAuthMode: getenv("ADMIN_AUTH_MODE", "session"),
		AdminProxySecret: os.Getenv("ADMIN_PROXY_SECRET"),
		AdminUserHeader: getenv("ADMIN_USER_HEADER", "X-Forwarded-User"),
		AdminRolesHeader: getenv("ADMIN_ROLES_HEADER", "X-Forwarded-Groups"),
		AdminSessionTTL: getenvDuration("ADMIN_SESSION_TTL", 12*time.Hour),
		AdminLoginMaxAttempts: getenvInt64("ADMIN_LOGIN_MAX_ATTEMPTS", 8),
		AdminLoginWindow: getenvDuration("ADMIN_LOGIN_WINDOW", 15*time.Minute),
		AdminBootstrapUsername: getenv("ADMIN_BOOTSTRAP_USERNAME", "admin"),
		AdminBootstrapPassword: os.Getenv("ADMIN_BOOTSTRAP_PASSWORD"),
		DefaultMaxOutputTokens: getenvInt64("DEFAULT_MAX_OUTPUT_TOKENS", 2048),
		StreamIncludeUsage: getenvBool("STREAM_INCLUDE_USAGE", true),
	}
	if cfg.DatabaseURL == "" { return Config{}, errors.New("DATABASE_URL is required") }
	if cfg.KeyHMACSecret == "" { return Config{}, errors.New("API_KEY_HMAC_SECRET is required") }
	return cfg, nil
}

func getenv(key, fallback string) string { if v := os.Getenv(key); v != "" { return v }; return fallback }

func getenvInt64(key string, fallback int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func getenvBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func defaultThinkWatchConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return dir + string(os.PathSeparator) + "ThinkWatch" + string(os.PathSeparator) + "config.yaml"
}
