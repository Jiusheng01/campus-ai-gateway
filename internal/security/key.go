package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

func Digest(secret, key string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil))
}

func NewAPIKey(secret string) (plain, prefix, digest string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil { return "", "", "", err }
	token := strings.TrimRight(base64.RawURLEncoding.EncodeToString(buf), "=")
	plain = "sk-campus-" + token
	if len(plain) > 16 { prefix = plain[:16] } else { prefix = plain }
	digest = Digest(secret, plain)
	return
}
