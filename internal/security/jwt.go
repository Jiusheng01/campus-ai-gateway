package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Self-contained HS256 JWT support for admin access tokens.
//
// The gateway deliberately does not pull in a JWT library: the token shape is
// fixed and small, and owning the parser keeps the validation rules explicit.
// Only HS256 is accepted; "alg":"none" and asymmetric algorithms are rejected
// outright, so the classic algorithm-confusion attack has no foothold.

const (
	// AdminTokenIssuer and AdminTokenAudience pin the tokens to this service.
	// A token minted for anything else fails verification.
	AdminTokenIssuer   = "campus-ai-gateway"
	AdminTokenAudience = "campus-admin"

	// TokenUseAccess marks a short-lived access token. Refresh tokens are not
	// JWTs at all, so there is only one value today.
	TokenUseAccess = "access"

	jwtHeaderJSON = `{"alg":"HS256","typ":"JWT"}`

	// clockSkewLeeway tolerates small clock differences between replicas when
	// checking not-before. It is not applied to expiry.
	clockSkewLeeway = 30 * time.Second
)

// Token validation sentinels. Callers switch on these to choose an HTTP status
// instead of matching error text.
var (
	ErrTokenMalformed        = errors.New("token is malformed")
	ErrTokenSignatureInvalid = errors.New("token signature is invalid")
	ErrTokenExpired          = errors.New("token has expired")
	ErrTokenNotYetValid      = errors.New("token is not valid yet")
	ErrTokenWrongUse         = errors.New("token has the wrong token_use")
	ErrTokenWrongIssuer      = errors.New("token issuer mismatch")
	ErrTokenWrongAudience    = errors.New("token audience mismatch")
	ErrTokenWrongAlgorithm   = errors.New("token algorithm is not supported")
)

// AdminAccessClaims is the payload of an admin access token.
//
// Roles and account status are intentionally absent. They are read from the
// database on every request so a role change or disable takes effect
// immediately rather than when the token expires.
type AdminAccessClaims struct {
	Subject         string // admin user id
	SessionID       string // gateway_admin_session.sid
	UserAuthVersion int64  // admin user auth_version at issue time
	SessionVersion  int64  // session version at issue time
	IssuedAt        time.Time
	NotBefore       time.Time
	ExpiresAt       time.Time
	JTI             string
}

// jwtPayload is the on-the-wire JSON. Field names are short to keep the token
// compact; they are the ones described in the design doc.
type jwtPayload struct {
	TokenUse  string `json:"token_use"`
	Subject   string `json:"sub"`
	Session   string `json:"sid"`
	UserAuth  int64  `json:"uv"`
	SessVer   int64  `json:"sv"`
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	IssuedAt  int64  `json:"iat"`
	NotBefore int64  `json:"nbf"`
	Expires   int64  `json:"exp"`
	JTI       string `json:"jti"`
}

type jwtHeaderFields struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// SignAdminAccessToken encodes and signs the claims with HS256.
func SignAdminAccessToken(secret []byte, c AdminAccessClaims) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("token secret is empty")
	}
	if c.Subject == "" || c.SessionID == "" {
		return "", errors.New("token subject and session id are required")
	}
	if c.JTI == "" {
		jti, err := NewRandomID(16)
		if err != nil {
			return "", err
		}
		c.JTI = jti
	}
	payload := jwtPayload{
		TokenUse:  TokenUseAccess,
		Subject:   c.Subject,
		Session:   c.SessionID,
		UserAuth:  c.UserAuthVersion,
		SessVer:   c.SessionVersion,
		Issuer:    AdminTokenIssuer,
		Audience:  AdminTokenAudience,
		IssuedAt:  c.IssuedAt.Unix(),
		NotBefore: c.NotBefore.Unix(),
		Expires:   c.ExpiresAt.Unix(),
		JTI:       c.JTI,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode token payload: %w", err)
	}
	signingInput := b64.EncodeToString([]byte(jwtHeaderJSON)) + "." + b64.EncodeToString(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	return signingInput + "." + b64.EncodeToString(mac.Sum(nil)), nil
}

// VerifyAdminAccessToken validates signature, algorithm, issuer, audience, use
// and time window, and returns the claims. Every failure mode maps to a
// sentinel error so the HTTP layer can pick the right status code.
func VerifyAdminAccessToken(secret []byte, token string) (AdminAccessClaims, error) {
	if len(secret) == 0 {
		return AdminAccessClaims{}, errors.New("token secret is empty")
	}
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return AdminAccessClaims{}, ErrTokenMalformed
	}

	headerRaw, err := b64.DecodeString(parts[0])
	if err != nil {
		return AdminAccessClaims{}, ErrTokenMalformed
	}
	var hdr jwtHeaderFields
	if err := json.Unmarshal(headerRaw, &hdr); err != nil {
		return AdminAccessClaims{}, ErrTokenMalformed
	}
	if hdr.Alg != "HS256" {
		return AdminAccessClaims{}, ErrTokenWrongAlgorithm
	}
	if hdr.Typ != "" && hdr.Typ != "JWT" {
		return AdminAccessClaims{}, ErrTokenMalformed
	}

	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return AdminAccessClaims{}, ErrTokenMalformed
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return AdminAccessClaims{}, ErrTokenSignatureInvalid
	}

	bodyRaw, err := b64.DecodeString(parts[1])
	if err != nil {
		return AdminAccessClaims{}, ErrTokenMalformed
	}
	var p jwtPayload
	if err := json.Unmarshal(bodyRaw, &p); err != nil {
		return AdminAccessClaims{}, ErrTokenMalformed
	}

	if p.Issuer != AdminTokenIssuer {
		return AdminAccessClaims{}, ErrTokenWrongIssuer
	}
	if !audienceMatches(p.Audience) {
		return AdminAccessClaims{}, ErrTokenWrongAudience
	}
	if p.TokenUse != TokenUseAccess {
		return AdminAccessClaims{}, ErrTokenWrongUse
	}
	if p.Subject == "" || p.Session == "" || p.JTI == "" {
		return AdminAccessClaims{}, ErrTokenMalformed
	}
	if p.Expires == 0 {
		return AdminAccessClaims{}, ErrTokenMalformed
	}

	now := time.Now()
	if now.After(time.Unix(p.Expires, 0)) {
		return AdminAccessClaims{}, ErrTokenExpired
	}
	if p.NotBefore != 0 && now.Add(clockSkewLeeway).Before(time.Unix(p.NotBefore, 0)) {
		return AdminAccessClaims{}, ErrTokenNotYetValid
	}

	return AdminAccessClaims{
		Subject:         p.Subject,
		SessionID:       p.Session,
		UserAuthVersion: p.UserAuth,
		SessionVersion:  p.SessVer,
		IssuedAt:        time.Unix(p.IssuedAt, 0),
		NotBefore:       time.Unix(p.NotBefore, 0),
		ExpiresAt:       time.Unix(p.Expires, 0),
		JTI:             p.JTI,
	}, nil
}

// audienceMatches accepts the single-string form we emit and the array form a
// strict reader might produce.
func audienceMatches(raw string) bool {
	if raw == AdminTokenAudience {
		return true
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return false
	}
	for _, a := range list {
		if a == AdminTokenAudience {
			return true
		}
	}
	return false
}

// --- refresh tokens --------------------------------------------------------

// Refresh tokens are opaque strings a client presents to rotate an access
// token. Two forms exist:
//
//   - Legacy "<session_id>.<secret>": the secret carries all the entropy and the
//     database stores SHA-256 of the whole string. It has no authentication of
//     its own beyond that hash, so it is accepted only when its hash matches
//     the stored one — a legacy token is "proven" by the database, never by
//     anything the client attached.
//
//   - Signed "v2.<session_id>.<secret>.<mac>": the MAC is
//     HMAC-SHA256(secret, label || session_id || secret) under a domain label
//     distinct from every other use of the admin session secret. The MAC is
//     what lets the gateway tell a genuine replay (a real token the holder
//     copied) from a forgery (a guessed "<sid>.<guess>" built from a leaked
//     session id): only someone who knows the signing secret can produce a
//     valid MAC, and guessing the secret is precluded by the stored hash.
//
// A forged legacy token and a forged v2 token are both rejected with 401 and
// revoke nothing. Only a MAC-valid v2 token that is neither the current hash
// nor a grace-window predecessor is treated as a replay and revokes the
// session family — because reaching that branch proves the bearer really held
// the token.
const (
	refreshSecretBytes = 32

	// refreshTokenV2Prefix marks the signed token form.
	refreshTokenV2Prefix = "v2."

	// refreshTokenMACLabel domain-separates the refresh-token MAC from the
	// rotation derivation and every other use of the admin session secret.
	// Reusing one key with distinct labels is the standard way to serve
	// several purposes without any of them being exchangeable.
	refreshTokenMACLabel = "refresh-token-mac-v2"

	// AdminRefreshRotationLabel domain-separates the rotation derivation from
	// every other use of the admin session secret.
	AdminRefreshRotationLabel = "refresh-rotation-v1"
)

// ErrRefreshTokenMalformed is returned when a refresh token does not parse or
// its v2 MAC does not verify.
var ErrRefreshTokenMalformed = errors.New("refresh token is malformed")

// AdminRefreshToken is a parsed, MAC-verified refresh token.
type AdminRefreshToken struct {
	SessionID string
	// Secret is the per-token secret half: the part after the session id and
	// before any trailing MAC.
	Secret string
	// Signed reports whether the v2 MAC was verified. A signed token proves
	// the bearer knows the signing secret; an unsigned legacy token must be
	// confirmed against the database hash before it is trusted.
	Signed bool
}

// NewAdminRefreshToken mints a signed v2 refresh token for a session and
// returns both the plaintext (to put in the cookie) and the hash (to persist).
func NewAdminRefreshToken(secret []byte, sessionID string) (plain, hash string, err error) {
	if len(secret) == 0 {
		return "", "", errors.New("refresh signing secret is empty")
	}
	if sessionID == "" {
		return "", "", errors.New("session id is required")
	}
	s, err := NewRandomID(refreshSecretBytes)
	if err != nil {
		return "", "", err
	}
	return wrapRefreshToken(secret, sessionID, s)
}

// wrapRefreshToken builds a signed v2 token from a session id and its secret
// half, returning the plaintext and the stored hash.
func wrapRefreshToken(secret []byte, sessionID, refreshSecret string) (plain, hash string, err error) {
	plain = refreshTokenV2Prefix + sessionID + "." + refreshSecret + "." + refreshTokenMAC(secret, sessionID, refreshSecret)
	return plain, HashAdminRefreshToken(plain), nil
}

// HashAdminRefreshToken returns the stored representation of a refresh token.
//
// It hashes the whole opaque string — MAC and all — so the stored value is tied
// to the exact bytes the client holds. A plain SHA-256 is enough: the secret
// half is 256 bits of CSPRNG output, not brute-forceable even if the hash
// leaks.
func HashAdminRefreshToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// SplitAdminRefreshToken validates the legacy "<sid>.<secret>" shape and
// returns the two halves. It is retained for the legacy branch of Parse and for
// tests that exercise the legacy form directly.
func SplitAdminRefreshToken(plain string) (sessionID, secret string, err error) {
	idx := strings.IndexByte(plain, '.')
	if idx <= 0 || idx == len(plain)-1 {
		return "", "", ErrRefreshTokenMalformed
	}
	sessionID, secret = plain[:idx], plain[idx+1:]
	if strings.ContainsAny(secret, ".") {
		return "", "", ErrRefreshTokenMalformed
	}
	return sessionID, secret, nil
}

// ParseAdminRefreshToken parses a presented refresh token and, for the signed
// v2 form, verifies the MAC.
//
//   - A v2 token with a valid MAC returns Signed == true: the bearer proved
//     knowledge of the signing secret, which an attacker who only learned the
//     session id cannot do.
//   - A v2 token with an invalid MAC, or any unparseable shape, returns
//     ErrRefreshTokenMalformed so the caller answers 401 and revokes nothing.
//   - A legacy "<sid>.<secret>" returns Signed == false; the caller must confirm
//     the secret against the stored hash before trusting it, and the first such
//     confirmation transparently upgrades the cookie to v2 on rotation.
func ParseAdminRefreshToken(secret []byte, plain string) (AdminRefreshToken, error) {
	plain = strings.TrimSpace(plain)
	if strings.HasPrefix(plain, refreshTokenV2Prefix) {
		rest := plain[len(refreshTokenV2Prefix):]
		sid, sec, mac, ok := splitRefreshParts(rest)
		if !ok {
			return AdminRefreshToken{}, ErrRefreshTokenMalformed
		}
		if !verifyRefreshTokenMAC(secret, sid, sec, mac) {
			return AdminRefreshToken{}, ErrRefreshTokenMalformed
		}
		return AdminRefreshToken{SessionID: sid, Secret: sec, Signed: true}, nil
	}
	sid, sec, err := SplitAdminRefreshToken(plain)
	if err != nil {
		return AdminRefreshToken{}, err
	}
	return AdminRefreshToken{SessionID: sid, Secret: sec, Signed: false}, nil
}

// splitRefreshParts splits "sid.secret.mac" into its three parts, requiring
// exactly two separators and no dot inside the trailing mac.
func splitRefreshParts(s string) (sid, secret, mac string, ok bool) {
	i := strings.IndexByte(s, '.')
	if i <= 0 {
		return "", "", "", false
	}
	j := strings.IndexByte(s[i+1:], '.')
	if j <= 0 {
		return "", "", "", false
	}
	sid = s[:i]
	secret = s[i+1 : i+1+j]
	mac = s[i+1+j+1:]
	if strings.ContainsAny(mac, ".") {
		return "", "", "", false
	}
	return sid, secret, mac, true
}

// refreshTokenMAC computes the v2 integrity tag over the session id and the
// token's secret half.
func refreshTokenMAC(secret []byte, sessionID, refreshSecret string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(refreshTokenMACLabel))
	mac.Write([]byte(sessionID))
	mac.Write([]byte{0})
	mac.Write([]byte(refreshSecret))
	return b64.EncodeToString(mac.Sum(nil))
}

// verifyRefreshTokenMAC checks a v2 MAC in constant time.
func verifyRefreshTokenMAC(secret []byte, sessionID, refreshSecret, mac string) bool {
	expected := refreshTokenMAC(secret, sessionID, refreshSecret)
	return hmac.Equal([]byte(expected), []byte(mac))
}

// DeriveNextAdminRefreshToken computes the signed v2 successor of a refresh
// token.
//
// The successor's secret is a pure function of (secret, session id, current
// secret), so the same presented token always yields byte-identical
// successors. That is what makes concurrent refreshes safe: when two browser
// tabs race with the same token, both derive the same next cookie, and the
// loser of the database race can still answer 200 with a token that matches
// what the winner stored, instead of clearing the cookie the winner just set.
func DeriveNextAdminRefreshToken(secret []byte, sessionID, currentSecret string) (plain, hash string, err error) {
	nextSecret, err := deriveNextRefreshSecret(secret, sessionID, currentSecret)
	if err != nil {
		return "", "", err
	}
	return wrapRefreshToken(secret, sessionID, nextSecret)
}

// deriveNextRefreshSecret is the bare deterministic successor secret, without
// the version prefix or the MAC. Callers wrap it for the wire format they need.
func deriveNextRefreshSecret(secret []byte, sessionID, currentSecret string) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("refresh rotation secret is empty")
	}
	if sessionID == "" || currentSecret == "" {
		return "", ErrRefreshTokenMalformed
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(AdminRefreshRotationLabel))
	mac.Write([]byte(sessionID))
	mac.Write([]byte{0})
	mac.Write([]byte(currentSecret))
	return b64.EncodeToString(mac.Sum(nil)), nil
}

// b64 is the unpadded base64url alphabet required by JWT.
var b64 = base64.RawURLEncoding

// NewRandomID returns n cryptographically random bytes encoded as base64url
// without padding. Used for refresh secrets and JWT ids.
func NewRandomID(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("random id length must be positive")
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return b64.EncodeToString(buf), nil
}
