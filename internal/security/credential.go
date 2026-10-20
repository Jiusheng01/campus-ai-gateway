package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Credential sealing: the gateway holds third-party provider credentials, so a
// database dump must not equal a vendor-key leak (ADR-0017). Values are sealed
// with AES-256-GCM under one process-wide key.
//
// The persisted form is "v1:" followed by the base64url encoding of
// nonce||ciphertext||tag. The version prefix is written but deliberately not
// interpreted: it does not select a key or an algorithm. It exists so a keyring
// can be introduced later without changing the column, at which point Open can
// dispatch on it.
const (
	// CredentialKeySize is the exact key length AES-256 requires.
	CredentialKeySize = 32

	credentialSealPrefix = "v1:"
	credentialNonceSize  = 12 // GCM's standard nonce length
)

var (
	// ErrCredentialKeySize says the configured key cannot be used for AES-256.
	ErrCredentialKeySize = errors.New("credential encryption key must be exactly 32 bytes")
	// ErrCredentialSealedFormat says a stored value is not in the sealed format.
	ErrCredentialSealedFormat = errors.New("credential ciphertext is not in the v1: sealed format")
)

// Seal encrypts plaintext into the stored credential form.
//
// A fresh random nonce is generated per call. Reusing a nonce under the same key
// would make GCM's confidentiality and integrity guarantees fail, so there is no
// caller-supplied nonce to get wrong.
func Seal(key, plaintext []byte) (string, error) {
	if len(key) != CredentialKeySize {
		return "", ErrCredentialKeySize
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, credentialNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return credentialSealPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Open decrypts a value produced by Seal. It authenticates the ciphertext, so a
// modified stored value returns an error rather than a different key.
func Open(key []byte, sealed string) ([]byte, error) {
	if len(key) != CredentialKeySize {
		return nil, ErrCredentialKeySize
	}
	if !strings.HasPrefix(sealed, credentialSealPrefix) {
		return nil, ErrCredentialSealedFormat
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(sealed, credentialSealPrefix))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCredentialSealedFormat, err)
	}
	if len(raw) < credentialNonceSize {
		return nil, ErrCredentialSealedFormat
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce, ciphertext := raw[:credentialNonceSize], raw[credentialNonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("open sealed credential: %w", err)
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential GCM: %w", err)
	}
	return gcm, nil
}
