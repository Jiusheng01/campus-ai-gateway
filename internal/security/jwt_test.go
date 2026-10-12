package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var testSecret = []byte("test-signing-secret-for-admin-access-tokens")

func validClaims() AdminAccessClaims {
	now := time.Now()
	return AdminAccessClaims{
		Subject:         "11111111-1111-1111-1111-111111111111",
		SessionID:       "22222222-2222-2222-2222-222222222222",
		UserAuthVersion: 3,
		SessionVersion:  1,
		IssuedAt:        now,
		NotBefore:       now,
		ExpiresAt:       now.Add(15 * time.Minute),
	}
}

// signRaw builds a token from arbitrary header/payload so negative cases can
// violate exactly one rule at a time.
func signRaw(t *testing.T, secret []byte, header string, payload map[string]any) string {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	input := b64.EncodeToString([]byte(header)) + "." + b64.EncodeToString(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(input))
	return input + "." + b64.EncodeToString(mac.Sum(nil))
}

func basePayload() map[string]any {
	now := time.Now()
	return map[string]any{
		"token_use": TokenUseAccess,
		"sub":       "11111111-1111-1111-1111-111111111111",
		"sid":       "22222222-2222-2222-2222-222222222222",
		"uv":        1,
		"sv":        1,
		"iss":       AdminTokenIssuer,
		"aud":       AdminTokenAudience,
		"iat":       now.Unix(),
		"nbf":       now.Unix(),
		"exp":       now.Add(15 * time.Minute).Unix(),
		"jti":       "abc123",
	}
}

const goodHeader = `{"alg":"HS256","typ":"JWT"}`

// --- happy path ------------------------------------------------------------

func TestSignAndVerifyAccessToken(t *testing.T) {
	tok, err := SignAdminAccessToken(testSecret, validClaims())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if strings.Count(tok, ".") != 2 {
		t.Fatalf("expected a 3-part JWT, got %q", tok)
	}
	claims, err := VerifyAdminAccessToken(testSecret, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("subject mismatch: %q", claims.Subject)
	}
	if claims.SessionID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("session mismatch: %q", claims.SessionID)
	}
	if claims.UserAuthVersion != 3 || claims.SessionVersion != 1 {
		t.Fatalf("version mismatch: uv=%d sv=%d", claims.UserAuthVersion, claims.SessionVersion)
	}
	if claims.JTI == "" {
		t.Fatal("jti must be populated")
	}
}

func TestSignGeneratesJTIWhenMissing(t *testing.T) {
	a, err := SignAdminAccessToken(testSecret, validClaims())
	if err != nil {
		t.Fatalf("sign a: %v", err)
	}
	b, err := SignAdminAccessToken(testSecret, validClaims())
	if err != nil {
		t.Fatalf("sign b: %v", err)
	}
	ca, _ := VerifyAdminAccessToken(testSecret, a)
	cb, _ := VerifyAdminAccessToken(testSecret, b)
	if ca.JTI == cb.JTI {
		t.Fatal("each token must get a unique jti")
	}
}

func TestSignRejectsEmptySecretOrSubject(t *testing.T) {
	if _, err := SignAdminAccessToken(nil, validClaims()); err == nil {
		t.Fatal("empty secret must be rejected")
	}
	c := validClaims()
	c.Subject = ""
	if _, err := SignAdminAccessToken(testSecret, c); err == nil {
		t.Fatal("empty subject must be rejected")
	}
	c = validClaims()
	c.SessionID = ""
	if _, err := SignAdminAccessToken(testSecret, c); err == nil {
		t.Fatal("empty session id must be rejected")
	}
}

// --- signature and algorithm ----------------------------------------------

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	tok, err := SignAdminAccessToken(testSecret, validClaims())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	parts := strings.Split(tok, ".")
	// Swap the payload for one claiming a different subject, keeping the old
	// signature.
	evil, _ := json.Marshal(basePayload())
	evil[0] = 'x'
	tampered := parts[0] + "." + b64.EncodeToString(evil) + "." + parts[2]
	if _, err := VerifyAdminAccessToken(testSecret, tampered); !errors.Is(err, ErrTokenSignatureInvalid) {
		t.Fatalf("expected ErrTokenSignatureInvalid, got %v", err)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	tok, err := SignAdminAccessToken(testSecret, validClaims())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := VerifyAdminAccessToken([]byte("a-different-secret"), tok); !errors.Is(err, ErrTokenSignatureInvalid) {
		t.Fatalf("expected ErrTokenSignatureInvalid, got %v", err)
	}
}

func TestVerifyRejectsNoneAlgorithm(t *testing.T) {
	p := basePayload()
	tok := signRaw(t, testSecret, `{"alg":"none","typ":"JWT"}`, p)
	if _, err := VerifyAdminAccessToken(testSecret, tok); !errors.Is(err, ErrTokenWrongAlgorithm) {
		t.Fatalf("alg=none must be rejected, got %v", err)
	}
}

func TestVerifyRejectsAsymmetricAlgorithm(t *testing.T) {
	for _, alg := range []string{"RS256", "ES256", "HS512", "HS384"} {
		header := `{"alg":"` + alg + `","typ":"JWT"}`
		tok := signRaw(t, testSecret, header, basePayload())
		if _, err := VerifyAdminAccessToken(testSecret, tok); !errors.Is(err, ErrTokenWrongAlgorithm) {
			t.Fatalf("alg=%s must be rejected, got %v", alg, err)
		}
	}
}

// --- time window -----------------------------------------------------------

func TestVerifyRejectsExpiredToken(t *testing.T) {
	now := time.Now()
	p := basePayload()
	p["iat"] = now.Add(-time.Hour).Unix()
	p["nbf"] = now.Add(-time.Hour).Unix()
	p["exp"] = now.Add(-time.Minute).Unix()
	tok := signRaw(t, testSecret, goodHeader, p)
	if _, err := VerifyAdminAccessToken(testSecret, tok); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func TestVerifyRejectsNotYetValidToken(t *testing.T) {
	now := time.Now()
	p := basePayload()
	// Well beyond the skew leeway.
	p["nbf"] = now.Add(10 * time.Minute).Unix()
	p["exp"] = now.Add(time.Hour).Unix()
	tok := signRaw(t, testSecret, goodHeader, p)
	if _, err := VerifyAdminAccessToken(testSecret, tok); !errors.Is(err, ErrTokenNotYetValid) {
		t.Fatalf("expected ErrTokenNotYetValid, got %v", err)
	}
}

func TestVerifyToleratesSmallClockSkewOnNotBefore(t *testing.T) {
	now := time.Now()
	p := basePayload()
	// Inside the leeway: must still be accepted.
	p["nbf"] = now.Add(10 * time.Second).Unix()
	p["exp"] = now.Add(time.Hour).Unix()
	tok := signRaw(t, testSecret, goodHeader, p)
	if _, err := VerifyAdminAccessToken(testSecret, tok); err != nil {
		t.Fatalf("skew within leeway must be accepted, got %v", err)
	}
}

// --- claim validation ------------------------------------------------------

func TestVerifyRejectsWrongIssuer(t *testing.T) {
	p := basePayload()
	p["iss"] = "some-other-service"
	tok := signRaw(t, testSecret, goodHeader, p)
	if _, err := VerifyAdminAccessToken(testSecret, tok); !errors.Is(err, ErrTokenWrongIssuer) {
		t.Fatalf("expected ErrTokenWrongIssuer, got %v", err)
	}
}

func TestVerifyRejectsWrongAudience(t *testing.T) {
	p := basePayload()
	p["aud"] = "some-other-audience"
	tok := signRaw(t, testSecret, goodHeader, p)
	if _, err := VerifyAdminAccessToken(testSecret, tok); !errors.Is(err, ErrTokenWrongAudience) {
		t.Fatalf("expected ErrTokenWrongAudience, got %v", err)
	}
}

func TestVerifyAcceptsAudienceArrayForm(t *testing.T) {
	p := basePayload()
	p["aud"] = `["` + AdminTokenAudience + `"]`
	tok := signRaw(t, testSecret, goodHeader, p)
	if _, err := VerifyAdminAccessToken(testSecret, tok); err != nil {
		t.Fatalf("audience array form must be accepted, got %v", err)
	}
}

func TestVerifyRejectsWrongTokenUse(t *testing.T) {
	p := basePayload()
	p["token_use"] = "refresh"
	tok := signRaw(t, testSecret, goodHeader, p)
	if _, err := VerifyAdminAccessToken(testSecret, tok); !errors.Is(err, ErrTokenWrongUse) {
		t.Fatalf("expected ErrTokenWrongUse, got %v", err)
	}
}

func TestVerifyRejectsMissingRequiredClaims(t *testing.T) {
	for _, field := range []string{"sub", "sid", "jti"} {
		p := basePayload()
		p[field] = ""
		tok := signRaw(t, testSecret, goodHeader, p)
		if _, err := VerifyAdminAccessToken(testSecret, tok); !errors.Is(err, ErrTokenMalformed) {
			t.Fatalf("missing %s: expected ErrTokenMalformed, got %v", field, err)
		}
	}
	p := basePayload()
	delete(p, "exp")
	tok := signRaw(t, testSecret, goodHeader, p)
	if _, err := VerifyAdminAccessToken(testSecret, tok); !errors.Is(err, ErrTokenMalformed) {
		t.Fatalf("missing exp: expected ErrTokenMalformed, got %v", err)
	}
}

// --- structural validation -------------------------------------------------

func TestVerifyRejectsMalformedTokens(t *testing.T) {
	cases := map[string]string{
		"empty":        "",
		"one part":     "abcdef",
		"two parts":    "aaaa.bbbb",
		"four parts":   "aaaa.bbbb.cccc.dddd",
		"empty header": ".bbbb.cccc",
		"empty body":   "aaaa..cccc",
		"empty sig":    "aaaa.bbbb.",
		"bad b64 hdr":  "!!!.bbbb.cccc",
		"bad b64 body": "aaaa.!!!.cccc",
		"bad b64 sig":  "aaaa.bbbb.!!!",
	}
	for name, tok := range cases {
		if _, err := VerifyAdminAccessToken(testSecret, tok); !errors.Is(err, ErrTokenMalformed) {
			t.Fatalf("%s: expected ErrTokenMalformed, got %v", name, err)
		}
	}
}

func TestVerifyRejectsEmptySecret(t *testing.T) {
	tok, err := SignAdminAccessToken(testSecret, validClaims())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := VerifyAdminAccessToken(nil, tok); err == nil {
		t.Fatal("verification with an empty secret must fail")
	}
}

// --- refresh tokens --------------------------------------------------------

func TestRefreshTokenRoundTrip(t *testing.T) {
	sid := "33333333-3333-3333-3333-333333333333"
	plain, hash, err := NewAdminRefreshToken(sid)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	gotSID, secret, err := SplitAdminRefreshToken(plain)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if gotSID != sid {
		t.Fatalf("session id mismatch: %q", gotSID)
	}
	if secret == "" {
		t.Fatal("secret must not be empty")
	}
	if hash != HashAdminRefreshToken(plain) {
		t.Fatal("hash must be deterministic for the same token")
	}
	if len(hash) != 64 {
		t.Fatalf("expected a 64-char sha256 hex digest, got %d chars", len(hash))
	}
	if strings.Contains(hash, plain) {
		t.Fatal("the stored hash must not contain the plaintext token")
	}
}

func TestRefreshTokenSecretIsUniquePerMint(t *testing.T) {
	sid := "33333333-3333-3333-3333-333333333333"
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		plain, _, err := NewAdminRefreshToken(sid)
		if err != nil {
			t.Fatalf("mint %d: %v", i, err)
		}
		if seen[plain] {
			t.Fatal("refresh tokens must not repeat")
		}
		seen[plain] = true
	}
}

func TestNewAdminRefreshTokenRequiresSessionID(t *testing.T) {
	if _, _, err := NewAdminRefreshToken(""); err == nil {
		t.Fatal("empty session id must be rejected")
	}
}

func TestSplitAdminRefreshTokenRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"no-dot-here",
		".leading-dot",
		"trailing-dot.",
		"too.many.dots",
		".",
	}
	for _, tok := range cases {
		if _, _, err := SplitAdminRefreshToken(tok); !errors.Is(err, ErrRefreshTokenMalformed) {
			t.Fatalf("%q: expected ErrRefreshTokenMalformed, got %v", tok, err)
		}
	}
}

func TestHashAdminRefreshTokenDiffersPerToken(t *testing.T) {
	a := HashAdminRefreshToken("sid.aaa")
	b := HashAdminRefreshToken("sid.bbb")
	if a == b {
		t.Fatal("different tokens must hash differently")
	}
}

// --- random ids ------------------------------------------------------------

func TestNewRandomIDLengthAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id, err := NewRandomID(16)
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if id == "" {
			t.Fatal("id must not be empty")
		}
		if strings.ContainsAny(id, "=+/") {
			t.Fatalf("id must be unpadded base64url, got %q", id)
		}
		if seen[id] {
			t.Fatal("random ids must not repeat")
		}
		seen[id] = true
	}
	if _, err := NewRandomID(0); err == nil {
		t.Fatal("zero length must be rejected")
	}
	if _, err := NewRandomID(-1); err == nil {
		t.Fatal("negative length must be rejected")
	}
}
