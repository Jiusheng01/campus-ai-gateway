package security

import (
	"bytes"
	"strings"
	"testing"
)

// testCredentialKey is 32 bytes, the length AES-256 requires.
func testCredentialKey() []byte { return bytes.Repeat([]byte{0x42}, CredentialKeySize) }

func TestSealOpenRoundTrip(t *testing.T) {
	key := testCredentialKey()
	plaintext := []byte("sk-live-0123456789")
	sealed, err := Seal(key, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	got, err := Open(key, sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip = %q, want %q", got, plaintext)
	}
}

// The stored form carries a version prefix so a keyring can be introduced later
// without a schema migration. The prefix is part of the persisted contract.
func TestSealWritesTheVersionPrefix(t *testing.T) {
	sealed, err := Seal(testCredentialKey(), []byte("sk-live"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !strings.HasPrefix(sealed, "v1:") {
		t.Fatalf("sealed value %q must start with the v1: prefix", sealed)
	}
}

// A nonce reused under one key makes the ciphertext deterministic and breaks
// GCM's security. Two seals of the same plaintext must differ.
func TestSealUsesAFreshNonce(t *testing.T) {
	key := testCredentialKey()
	first, err := Seal(key, []byte("sk-live"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := Seal(key, []byte("sk-live"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if first == second {
		t.Fatal("two seals of the same plaintext produced identical ciphertext; the nonce is not random")
	}
}

// GCM's authentication tag is what makes tampering detectable: a stored value
// changed by someone with database access must not decrypt to a key the gateway
// then sends upstream.
func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	key := testCredentialKey()
	sealed, err := Seal(key, []byte("sk-live"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// Flip one byte of the base64 payload, leaving the prefix intact.
	body := []byte(sealed[len("v1:"):])
	idx := len(body) / 2
	if body[idx] == 'A' {
		body[idx] = 'B'
	} else {
		body[idx] = 'A'
	}
	if _, err := Open(key, "v1:"+string(body)); err == nil {
		t.Fatal("a tampered ciphertext must not open")
	}
}

func TestOpenRejectsTheWrongKey(t *testing.T) {
	sealed, err := Seal(testCredentialKey(), []byte("sk-live"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	wrong := bytes.Repeat([]byte{0x43}, CredentialKeySize)
	if _, err := Open(wrong, sealed); err == nil {
		t.Fatal("a different key must not open the ciphertext")
	}
}

func TestSealRequiresAnAES256Key(t *testing.T) {
	if _, err := Seal([]byte("too-short"), []byte("sk-live")); err == nil {
		t.Fatal("a key that is not 32 bytes must be refused")
	}
}

func TestOpenRejectsAnUnknownFormat(t *testing.T) {
	key := testCredentialKey()
	for _, sealed := range []string{"", "v2:abc", "not-a-sealed-value"} {
		if _, err := Open(key, sealed); err == nil {
			t.Fatalf("%q must not open", sealed)
		}
	}
}
