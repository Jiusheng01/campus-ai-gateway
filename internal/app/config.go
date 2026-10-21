package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// AdminSessionSecretMinBytes is the minimum length accepted for an explicitly
// configured ADMIN_SESSION_SECRET. Anything shorter is rejected at startup
// rather than silently weakening access-token signing.
const AdminSessionSecretMinBytes = 32

// knownPlaceholderSecrets are the values that ship in example configuration:
// .env.example, deploy/docker-compose.yml and the documentation. None of them
// is a secret. A deployment that leaves one in place looks healthy until the
// day it is replaced — and replacing API_KEY_HMAC_SECRET invalidates every
// stored API-key digest at once — so startup refuses them by name.
//
// The comparison is case-insensitive because these values are copied by hand,
// and an operator who types CHANGEME meant the same thing.
var knownPlaceholderSecrets = []string{
	"change-me-in-production",
	"replace-with-a-long-random-secret",
	"replace-with-a-long-random-admin-token",
	"changeme",
}

// isPlaceholderSecret reports whether value is one of the shipped examples.
func isPlaceholderSecret(value string) bool {
	trimmed := strings.TrimSpace(value)
	for _, placeholder := range knownPlaceholderSecrets {
		if strings.EqualFold(trimmed, placeholder) {
			return true
		}
	}
	return false
}

// placeholderSecretError builds the startup refusal for a secret still set to a
// shipped example. Every refusal names the variable, quotes the offending value
// and says how to produce a usable one; consequence, when set, states what
// replacing the value later costs.
func placeholderSecretError(variable, value, replacement, consequence string) error {
	message := variable + " is set to a known placeholder (" + strings.TrimSpace(value) + "); " + replacement
	if consequence != "" {
		message += ". " + consequence
	}
	return errors.New(message)
}

// apiKeyHMACSecretMinBytes is the shortest API_KEY_HMAC_SECRET accepted at
// startup. The secret is the only input to every API-key digest, so a short one
// can be brute-forced offline against a leaked digest.
const apiKeyHMACSecretMinBytes = 16

// adminSessionDerivationLabel domain-separates the development fallback key.
// It guarantees the derived value differs from API_KEY_HMAC_SECRET and from
// every other HMAC the gateway computes with that secret.
const adminSessionDerivationLabel = "campus-admin-session-v1"

// credentialEncryptionDerivationLabel domain-separates the development fallback
// for sealing provider credentials. It keeps that key distinct from the
// API-key HMAC and from the administrator-session key, so a compromise of one
// purpose does not hand over the other.
const credentialEncryptionDerivationLabel = "credential-encryption-v1"

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

// The data plane.
//
// There is exactly one. "direct" resolves the model against
// gateway_model_route and dials the channel in-process, so a deployment that
// has exactly one API key does not have to run a second process to make a
// single call.
const DataPlaneDirect = "direct"

type Config struct {
	HTTPAddr      string
	DatabaseURL   string
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	KeyHMACSecret string

	// DataPlane is the resolved data plane. It is derived in LoadConfig from
	// DATA_PLANE so no caller has to re-derive the rule.
	//
	// A zero value reads as direct: the direct plane is the only one, so a
	// Config literal that never went through LoadConfig still describes the
	// plane this gateway actually runs.
	DataPlane string

	// AdminToken is the break-glass credential. It only takes effect when
	// ADMIN_AUTH_MODE is "token"; in "session" mode it is ignored so a stale
	// environment variable cannot silently bypass the login system.
	AdminToken       string
	AdminAuthMode    string
	AdminProxySecret string
	AdminUserHeader  string
	AdminRolesHeader string

	// AdminProxyCIDRs limits which peers may present the trusted-header
	// credentials at all.
	//
	// In trusted-header mode the only thing standing between an anonymous
	// caller and super_admin is one shared string. Anyone who can reach the
	// port and learns that string is an administrator, so the peer address is
	// a second, independent condition rather than a nicety. Leaving the list
	// empty keeps the historical behaviour — any peer may try — which is why
	// startup says so out loud instead of failing.
	AdminProxyCIDRs []string

	// Admin session authentication.
	AdminAccessTTL        time.Duration // lifetime of an access token
	AdminRefreshTTL       time.Duration // lifetime of a session and its refresh cookie
	AdminLoginMaxAttempts int64         // failed attempts allowed per window
	AdminLoginWindow      time.Duration // length of the login rate-limit window
	AdminCookieSecure     bool          // add Secure to the refresh cookie (needs HTTPS)
	AdminSetupLocalOnly   bool          // first-run setup is localhost-only
	TrustedProxyCIDRs     []string      // proxies whose X-Forwarded-For we trust

	// ProviderHTTPAllowlist names the hosts and address ranges that may be
	// reached over plaintext http://. It is empty by default, which rejects
	// every http:// Provider target; https:// never consults it.
	//
	// The same predicate is applied when base_url is saved and again to the
	// target URL before a request dials it, so a pre-existing row and a host
	// that was removed from the list are both refused. Keeping it an explicit
	// list keeps plaintext egress an exception a deployment opts into rather
	// than the default.
	ProviderHTTPAllowlist []string

	// AdminPublicOrigin is the browser-facing origin of the console, such as
	// "https://gateway.example.edu". It is optional and only consulted by the
	// same-origin check that guards the cookie-authenticated endpoints.
	//
	// It exists for proxies that rewrite Host to something the browser never
	// said — an internal service name, an upstream port — where comparing the
	// Origin header against the request's own authority can never match and
	// every refresh would look like a cross-site request. Setting it gives the
	// gateway a second, explicit authority to accept.
	AdminPublicOrigin      string
	AdminBootstrapUsername string // optional non-interactive bootstrap
	AdminBootstrapPassword string

	// AdminSessionSecret is the dedicated key for administrator session
	// cryptography (access-token HS256 signing and refresh rotation). Keeping
	// it separate from API_KEY_HMAC_SECRET stops a compromise of one security
	// domain from reaching the other. When empty, a domain-separated key is
	// derived from API_KEY_HMAC_SECRET so local development needs no extra
	// configuration; production is expected to set it explicitly.
	AdminSessionSecret string

	// CredentialEncryptionKey seals provider credentials stored inline
	// (ADR-0017). When empty, a domain-separated key is derived from
	// API_KEY_HMAC_SECRET so local development needs no extra configuration;
	// production is expected to set it explicitly.
	CredentialEncryptionKey string

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

	// PersonalKeyLimit is the maximum number of API Keys one Account may own.
	// It bounds a personal credential's blast radius and the size of the My API
	// Keys page. A non-positive value reads as the default — see
	// personalKeyLimitDefault — because a zero limit would silently disable key
	// creation for every member.
	PersonalKeyLimit int64
}

// personalKeyLimitDefault is the per-Account key ceiling when
// PERSONAL_KEY_LIMIT is unset. Ten keys is enough for a handful of
// applications and small enough that the list stays reviewable.
const personalKeyLimitDefault int64 = 10

func LoadConfig() (Config, error) {
	cfg := Config{
		HTTPAddr:      getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		RedisAddr:     getenv("REDIS_ADDR", "redis:6379"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		RedisDB:       int(getenvInt64("REDIS_DB", 0)),
		KeyHMACSecret: os.Getenv("API_KEY_HMAC_SECRET"),

		AdminToken:       os.Getenv("ADMIN_TOKEN"),
		AdminAuthMode:    getenv("ADMIN_AUTH_MODE", "session"),
		AdminProxySecret: os.Getenv("ADMIN_PROXY_SECRET"),
		AdminUserHeader:  getenv("ADMIN_USER_HEADER", "X-Forwarded-User"),
		AdminRolesHeader: getenv("ADMIN_ROLES_HEADER", "X-Forwarded-Groups"),
		AdminProxyCIDRs:  splitCSV(os.Getenv("ADMIN_PROXY_CIDRS")),

		AdminAccessTTL:         getenvDuration("ADMIN_ACCESS_TTL", 15*time.Minute),
		AdminRefreshTTL:        getenvDuration("ADMIN_REFRESH_TTL", 30*24*time.Hour),
		AdminLoginMaxAttempts:  getenvInt64("ADMIN_LOGIN_MAX_ATTEMPTS", 8),
		AdminLoginWindow:       getenvDuration("ADMIN_LOGIN_WINDOW", 15*time.Minute),
		AdminCookieSecure:      getenvBool("ADMIN_COOKIE_SECURE", false),
		AdminSetupLocalOnly:    getenvBool("ADMIN_SETUP_LOCAL_ONLY", true),
		TrustedProxyCIDRs:      splitCSV(os.Getenv("TRUSTED_PROXY_CIDRS")),
		ProviderHTTPAllowlist:  splitCSV(os.Getenv("PROVIDER_HTTP_ALLOWLIST")),
		AdminBootstrapUsername: strings.TrimSpace(os.Getenv("ADMIN_BOOTSTRAP_USERNAME")),
		AdminBootstrapPassword: os.Getenv("ADMIN_BOOTSTRAP_PASSWORD"),

		AdminSessionSecret:      os.Getenv("ADMIN_SESSION_SECRET"),
		CredentialEncryptionKey: os.Getenv("CREDENTIAL_ENCRYPTION_KEY"),
		AdminRefreshReplayGrace: getenvDuration("ADMIN_REFRESH_REPLAY_GRACE", adminRefreshReplayGraceDefault),

		PublicRegistrationEnabled: getenvBool("PUBLIC_REGISTRATION_ENABLED", false),
		PublicRegisterMaxAttempts: getenvInt64("PUBLIC_REGISTER_MAX_ATTEMPTS", 5),
		PublicRegisterWindow:      getenvDuration("PUBLIC_REGISTER_WINDOW", time.Hour),

		PersonalKeyLimit: getenvInt64("PERSONAL_KEY_LIMIT", personalKeyLimitDefault),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.KeyHMACSecret == "" {
		return Config{}, errors.New("API_KEY_HMAC_SECRET is required")
	}
	if isPlaceholderSecret(cfg.KeyHMACSecret) {
		return Config{}, placeholderSecretError("API_KEY_HMAC_SECRET", cfg.KeyHMACSecret,
			"set a random value instead, for example from `openssl rand -base64 48`",
			"Changing this secret later invalidates every stored API-key digest, so all API keys would have to be recreated")
	}
	if len(strings.TrimSpace(cfg.KeyHMACSecret)) < apiKeyHMACSecretMinBytes {
		return Config{}, errors.New("API_KEY_HMAC_SECRET must be at least 16 bytes; " +
			"set a random value, for example from `openssl rand -base64 48`. " +
			"Changing this secret later invalidates every stored API-key digest, so all API keys would have to be recreated")
	}
	if isPlaceholderSecret(cfg.CredentialEncryptionKey) {
		return Config{}, placeholderSecretError("CREDENTIAL_ENCRYPTION_KEY", cfg.CredentialEncryptionKey,
			"set a random value instead, for example from `openssl rand -base64 48`",
			"Changing this key later makes every stored inline credential unreadable")
	}
	dataPlane, err := resolveDataPlane(os.Getenv("DATA_PLANE"))
	if err != nil {
		return Config{}, err
	}
	cfg.DataPlane = dataPlane
	if s := strings.TrimSpace(cfg.AdminSessionSecret); s != "" && len(s) < AdminSessionSecretMinBytes {
		return Config{}, errors.New("ADMIN_SESSION_SECRET must be at least 32 bytes of entropy")
	}
	if raw := strings.TrimSpace(os.Getenv("ADMIN_PUBLIC_ORIGIN")); raw != "" {
		origin, err := normalizePublicOrigin(raw)
		if err != nil {
			return Config{}, err
		}
		cfg.AdminPublicOrigin = origin
	}
	if cfg.AdminRefreshReplayGrace <= 0 {
		return Config{}, errors.New("ADMIN_REFRESH_REPLAY_GRACE must be positive")
	}
	// PERSONAL_KEY_LIMIT=0 or a negative value is a configuration mistake, not
	// a request to forbid keys. Fall back to the default rather than turning
	// every member's create button into a refusal.
	if cfg.PersonalKeyLimit <= 0 {
		cfg.PersonalKeyLimit = personalKeyLimitDefault
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
	case "token":
		// Only token mode reads ADMIN_TOKEN. The other modes ignore a stale
		// value on purpose, so validating it there would stop a deployment
		// over a variable it never uses.
		if isPlaceholderSecret(cfg.AdminToken) {
			return Config{}, placeholderSecretError("ADMIN_TOKEN", cfg.AdminToken,
				"set a random break-glass token instead, for example from `openssl rand -base64 48`", "")
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
	for _, cidr := range cfg.AdminProxyCIDRs {
		if _, _, err := parseCIDROrIP(cidr); err != nil {
			return Config{}, errors.New("ADMIN_PROXY_CIDRS contains an invalid entry: " + cidr)
		}
	}
	for _, entry := range cfg.ProviderHTTPAllowlist {
		if _, err := parseHostAllowlist([]string{entry}); err != nil {
			return Config{}, errors.New("PROVIDER_HTTP_ALLOWLIST contains an invalid entry: " + entry)
		}
	}
	// The same predicate the request path uses, so the rule applied at load
	// time and the rule applied per request cannot disagree about what this
	// mode is.
	if cfg.AdminAuthModeTrustedHeader() {
		// This mode trusts one shared string for the whole control plane. A
		// short or absent secret is not a weak configuration, it is an open
		// door, and it is worth refusing to start over — an operator who
		// meant to enable the mode will read the error, while one who set it
		// by accident gets a loud stop instead of a silent takeover.
		if isPlaceholderSecret(cfg.AdminProxySecret) {
			return Config{}, placeholderSecretError("ADMIN_PROXY_SECRET", cfg.AdminProxySecret,
				"set a random shared secret of at least "+strconv.Itoa(minAdminProxySecretLen)+
					" characters instead, for example from `openssl rand -hex "+strconv.Itoa(minAdminProxySecretLen/2)+"`", "")
		}
		if len(cfg.AdminProxySecret) < minAdminProxySecretLen {
			return Config{}, errors.New("ADMIN_PROXY_SECRET must be at least " + strconv.Itoa(minAdminProxySecretLen) + " characters in trusted-header mode")
		}
	}
	return cfg, nil
}

// minAdminProxySecretLen is the shortest shared secret trusted-header mode will
// accept. It matches the length a generated 128-bit hex token occupies, so the
// documented way of producing one always satisfies it.
const minAdminProxySecretLen = 32

// normalizePublicOrigin validates an ADMIN_PUBLIC_ORIGIN value and reduces it
// to the scheme://authority form the same-origin check compares against.
//
// A path, query or fragment is dropped rather than rejected: an operator who
// pastes the console URL — "https://gateway.example.edu/admin-ui/" — means the
// origin of that URL, and refusing it would be a trap. A missing scheme or
// authority is a real mistake and fails startup instead of silently
// disconnecting the CSRF check from what the operator intended.
func normalizePublicOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("ADMIN_PUBLIC_ORIGIN is not a valid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("ADMIN_PUBLIC_ORIGIN must start with http:// or https://")
	}
	if u.Host == "" {
		return "", errors.New("ADMIN_PUBLIC_ORIGIN must include a host")
	}
	return scheme + "://" + u.Host, nil
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

// AdminAuthModeTrustedHeader reports whether identities come from an
// authenticating reverse proxy rather than from the gateway's own login.
func (c Config) AdminAuthModeTrustedHeader() bool {
	return strings.EqualFold(strings.TrimSpace(c.AdminAuthMode), "trusted-header")
}

// DataPlaneMode returns the resolved mode, defaulting to direct: the direct
// plane is the only one, so a Config that never filled the field still
// describes the plane this gateway runs.
func (c Config) DataPlaneMode() string {
	if c.DataPlane == "" {
		return DataPlaneDirect
	}
	return c.DataPlane
}

// resolveDataPlane turns DATA_PLANE into one answer.
//
// The empty value and "auto" mean direct: the in-process plane is the only data
// plane, so doing nothing gets the one thing there is.
//
// The retired modes are refused by name rather than falling into the
// "must be one of" branch. An operator whose environment still names the
// removed serving plane needs to be told that the mode is gone — a bare list of
// valid values would read as a typo, and they would go looking for a spelling
// mistake instead of deleting the variable.
func resolveDataPlane(requested string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(requested))
	switch mode {
	case "", "auto", DataPlaneDirect:
		return DataPlaneDirect, nil
	case "think" + "watch", "shadow":
		return "", errors.New("DATA_PLANE=" + mode +
			" was removed; the direct data plane is the only data plane")
	default:
		return "", errors.New("DATA_PLANE must be one of auto or direct")
	}
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

// CredentialSealingKey returns the 32-byte AES-256 key used to seal inline
// provider credentials.
//
// A configured CREDENTIAL_ENCRYPTION_KEY is hashed to exactly 32 bytes, so an
// operator can supply the usual long random string (for example
// `openssl rand -base64 48`) without having to produce a raw 32-byte value. The
// returned bool reports whether the development fallback was used, so startup
// can warn. The fallback mirrors AdminSessionKey: a labelled HMAC over
// API_KEY_HMAC_SECRET, which domain-separates credential encryption from every
// other use of that secret.
func (c Config) CredentialSealingKey() ([]byte, bool) {
	if s := strings.TrimSpace(c.CredentialEncryptionKey); s != "" {
		sum := sha256.Sum256([]byte(s))
		return sum[:], false
	}
	mac := hmac.New(sha256.New, []byte(c.KeyHMACSecret))
	mac.Write([]byte(credentialEncryptionDerivationLabel))
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
