package app

import (
	"errors"
	"os"
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
	}
	if cfg.DatabaseURL == "" { return Config{}, errors.New("DATABASE_URL is required") }
	if cfg.KeyHMACSecret == "" { return Config{}, errors.New("API_KEY_HMAC_SECRET is required") }
	return cfg, nil
}

func getenv(key, fallback string) string { if v := os.Getenv(key); v != "" { return v }; return fallback }

func defaultThinkWatchConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return dir + string(os.PathSeparator) + "ThinkWatch" + string(os.PathSeparator) + "config.yaml"
}