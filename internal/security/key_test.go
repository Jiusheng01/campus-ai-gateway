package security

import "testing"

func TestNewAPIKeyAndDigest(t *testing.T) {
	plain, prefix, digest, err := NewAPIKey("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	if plain == "" || prefix == "" || digest == "" {
		t.Fatal("expected non-empty key material")
	}
	if got := Digest("test-secret", plain); got != digest {
		t.Fatalf("digest mismatch: got %s want %s", got, digest)
	}
	if got := Digest("different-secret", plain); got == digest {
		t.Fatal("digest must depend on the server secret")
	}
}
