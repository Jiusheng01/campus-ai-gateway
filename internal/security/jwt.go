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
	Subject         string    // admin user id
	SessionID       string    // gateway_admin_session.sid
	UserAuthVersion int64     // admin user auth_version at issue time
	SessionVersion  int64     // session version at issue time
	IssuedAt        time.Time
	NotBefore       time.Time
	ExpiresAt       time.Time
	JTI             string
}

// jwtPayload is the on-the-wire JSON. Field names are short to keep the token
// compact; they are the ones described in the design doc.
type jwtPayload struct {
	TokenUse string `json:"token_use"`
	Subject  string `json:"sub"`
	Session  string `json:"sid"`
	UserAuth int64  `json:"uv"`
	SessVer  int64  `json:"sv"`
	Issuer   string `json:"iss"`
	Audience string `json:"aud"`
	IssuedAt int64  `json:"iat"`
	NotBefore int64 `json:"nbf"`
	Expires  int64  `json:"exp"`
	JTI      string `json:"jti"`
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

// Refresh token format: "<session_id>.<secret>".
//
// Only the secret carries entropy; the session id is an identifier, not a
// secret, and is stored in the cookie alongside it so the server can look the
// session up without scanning the table. The database stores SHA-256 of the
// full token, never the token itself.
const refreshSecretBytes = 32

// ErrRefreshTokenMalformed is returned when a refresh token does not have the
// "<sid>.<secret>" shape.
var ErrRefreshTokenMalformed = errors.New("refresh token is malformed")

// NewAdminRefreshToken mints a refresh token for a session and returns both the
// plaintext (to put in the cookie) and the hash (to persist).
func NewAdminRefreshToken(sessionID string) (plain, hash string, err error) {
	if sessionID == "" {
		return "", "", errors.New("session id is required")
	}
	secret, err := NewRandomID(refreshSecretBytes)
	if err != nil {
		return "", "", err
	}
	plain = sessionID + "." + secret
	return plain, HashAdminRefreshToken(plain), nil
}

// HashAdminRefreshToken returns the stored representation of a refresh token.
//
// A plain SHA-256 is enough here: the secret is 256 bits of CSPRNG output, so
// it is not brute-forceable even if the hash leaks. Keying the hash would only
// couple session validity to the signing secret for no security gain.
func HashAdminRefreshToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// SplitAdminRefreshToken validates the shape and returns the two halves.
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
