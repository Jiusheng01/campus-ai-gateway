package security

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// testParams keeps Argon2id cheap so the suite stays fast. The production
// policy is exercised separately in TestDefaultParamsRoundTrip.
func testParams() PasswordParams {
	return PasswordParams{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
}

func TestHashPasswordProducesArgon2idPHC(t *testing.T) {
	h, err := HashPasswordWithParams("correct horse battery staple", testParams())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !IsArgon2idHash(h) {
		t.Fatalf("expected argon2id hash, got %q", h)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("unexpected PHC header: %q", h)
	}
	if got := strings.Count(h, "$"); got != 5 {
		t.Fatalf("expected 5 separators in PHC string, got %d: %q", got, h)
	}
}

func TestHashPasswordUsesFreshSalt(t *testing.T) {
	a, err := HashPasswordWithParams("same-password", testParams())
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	b, err := HashPasswordWithParams("same-password", testParams())
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	if a == b {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword(""); !errors.Is(err, ErrPasswordEmpty) {
		t.Fatalf("expected ErrPasswordEmpty, got %v", err)
	}
}

func TestVerifyPasswordAcceptsCorrect(t *testing.T) {
	// Use the production policy so the "no rehash needed" branch is exercised.
	h, err := HashPassword("s3cret-passphrase")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, rehash, err := VerifyPassword(h, "s3cret-passphrase")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("correct password must verify")
	}
	if rehash {
		t.Fatal("hash written with the current policy must not need rehash")
	}
}

func TestVerifyPasswordRejectsWrong(t *testing.T) {
	h, err := HashPasswordWithParams("right-password", testParams())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, _, err := VerifyPassword(h, "wrong-password")
	if err != nil {
		t.Fatalf("a wrong password is not an error, got %v", err)
	}
	if ok {
		t.Fatal("wrong password must not verify")
	}
}

func TestVerifyPasswordUnicodeAndLongInput(t *testing.T) {
	cases := []string{
		"密码-能小智-2026",
		"emoji-🔐-pass",
		strings.Repeat("a", 300),
		strings.Repeat("密码", 100),
	}
	for _, plain := range cases {
		h, err := HashPasswordWithParams(plain, testParams())
		if err != nil {
			t.Fatalf("hash %q: %v", plain, err)
		}
		ok, _, err := VerifyPassword(h, plain)
		if err != nil {
			t.Fatalf("verify %q: %v", plain, err)
		}
		if !ok {
			t.Fatalf("roundtrip failed for %q", plain)
		}
	}
}

func TestDefaultParamsRoundTrip(t *testing.T) {
	h, err := HashPassword("owasp-parameters")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.Contains(h, "m=19456,t=2,p=1") {
		t.Fatalf("default policy must be m=19456,t=2,p=1, got %q", h)
	}
	ok, rehash, err := VerifyPassword(h, "owasp-parameters")
	if err != nil || !ok {
		t.Fatalf("verify with default params: ok=%v err=%v", ok, err)
	}
	if rehash {
		t.Fatal("default-policy hash must not need rehash")
	}
}

// --- legacy bcrypt compatibility -------------------------------------------

func TestVerifyPasswordAcceptsBcryptAndRequestsRehash(t *testing.T) {
	for _, cost := range []int{bcrypt.MinCost, bcrypt.DefaultCost} {
		legacy, err := bcrypt.GenerateFromPassword([]byte("legacy-password"), cost)
		if err != nil {
			t.Fatalf("bcrypt generate: %v", err)
		}
		ok, rehash, err := VerifyPassword(string(legacy), "legacy-password")
		if err != nil {
			t.Fatalf("verify bcrypt: %v", err)
		}
		if !ok {
			t.Fatal("bcrypt password must verify")
		}
		if !rehash {
			t.Fatal("bcrypt hashes must be flagged for upgrade to Argon2id")
		}
		if IsArgon2idHash(string(legacy)) {
			t.Fatal("bcrypt hash misdetected as argon2id")
		}
	}
}

func TestVerifyPasswordRejectsWrongBcryptPassword(t *testing.T) {
	legacy, err := bcrypt.GenerateFromPassword([]byte("legacy-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt generate: %v", err)
	}
	ok, rehash, err := VerifyPassword(string(legacy), "not-the-password")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || rehash {
		t.Fatalf("wrong bcrypt password: ok=%v rehash=%v", ok, rehash)
	}
}

func TestBcryptPrefixVariantsDetected(t *testing.T) {
	// $2a$ / $2b$ / $2y$ all appear in existing databases.
	legacy, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt generate: %v", err)
	}
	body := string(legacy)[4:] // strip "$2a$"
	for _, prefix := range []string{"$2a$", "$2b$", "$2y$"} {
		if !IsBcryptHash(prefix + body) {
			t.Fatalf("prefix %s not recognised as bcrypt", prefix)
		}
	}
	if IsBcryptHash("$argon2id$v=19$m=1,t=1,p=1$aa$bb") {
		t.Fatal("argon2id misdetected as bcrypt")
	}
}

func TestVerifyPasswordBcryptWithBadCostIsMalformed(t *testing.T) {
	// bcrypt cost 99 is out of range; comparison must fail as malformed rather
	// than silently returning "wrong password".
	bad := "$2a$99$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ012345"
	_, _, err := VerifyPassword(bad, "pw")
	if err == nil {
		t.Fatal("expected an error for an unparsable bcrypt hash")
	}
}

// --- malformed / unsupported input -----------------------------------------

func TestVerifyPasswordEmptyHash(t *testing.T) {
	if _, _, err := VerifyPassword("", "pw"); !errors.Is(err, ErrPasswordHashMalformed) {
		t.Fatalf("expected ErrPasswordHashMalformed, got %v", err)
	}
}

func TestVerifyPasswordUnsupportedAlgorithm(t *testing.T) {
	cases := []string{
		"$argon2i$v=19$m=65536,t=3,p=1$c29tZXNhbHQ$aGFzaA",
		"$argon2d$v=19$m=65536,t=3,p=1$c29tZXNhbHQ$aGFzaA",
		"$scrypt$ln=16,r=8,p=1$c29tZXNhbHQ$aGFzaA",
		"$pbkdf2-sha256$29000$c2FsdA$aGFzaA",
		"plaintext-password",
	}
	for _, encoded := range cases {
		if _, _, err := VerifyPassword(encoded, "pw"); !errors.Is(err, ErrPasswordHashUnsupported) {
			t.Fatalf("%q: expected ErrPasswordHashUnsupported, got %v", encoded, err)
		}
	}
}

func TestVerifyPasswordMalformedArgon2id(t *testing.T) {
	cases := []string{
		"$argon2id$v=19$m=8192,t=1,p=1$c2FsdA",                 // missing key field
		"$argon2id$v=19$m=8192,t=1,p=1$c2FsdA$aGFzaA$extra",    // trailing field
		"$argon2id$v=19$m=8192,t=1$c2FsdA$aGFzaA",              // missing p=
		"$argon2id$v=19$m=0,t=1,p=1$c2FsdA$aGFzaA",             // zero memory
		"$argon2id$v=19$m=8192,t=0,p=1$c2FsdA$aGFzaA",          // zero iterations
		"$argon2id$v=19$m=8192,t=1,p=0$c2FsdA$aGFzaA",          // zero parallelism
		"$argon2id$v=19$m=8192,t=1,p=1$$aGFzaA",                // empty salt
		"$argon2id$v=19$m=8192,t=1,p=1$c2FsdA$",                // empty key
		"$argon2id$v=19$m=8192,t=1,p=1$not-base64!!$aGFzaA",    // bad base64
		"$argon2id$v=19$m=8192,t=1,p=1$c2FsdA$not-base64!!",    // bad base64 key
		"argon2id$v=19$m=8192,t=1,p=1$c2FsdA$aGFzaA",           // missing leading $
		"$argon2id$m=8192,t=1,p=1$c2FsdA$aGFzaA",               // missing version
		"$argon2id$v=16$m=8192,t=1,p=1$c2FsdA$aGFzaA",          // old argon2 version
		"$argon2id$v=19$memory=8192,t=1,p=1$c2FsdA$aGFzaA",     // wrong param name
	}
	for _, encoded := range cases {
		_, _, err := VerifyPassword(encoded, "pw")
		if err == nil {
			t.Fatalf("%q: expected an error", encoded)
		}
		if !errors.Is(err, ErrPasswordHashMalformed) && !errors.Is(err, ErrPasswordHashUnsupported) {
			t.Fatalf("%q: unexpected error kind: %v", encoded, err)
		}
	}
}

// --- rehash policy ---------------------------------------------------------

func TestWeakArgon2idParamsRequestRehash(t *testing.T) {
	weak := []PasswordParams{
		{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},  // weak memory
		{Memory: 19 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}, // weak iterations
		{Memory: 19 * 1024, Iterations: 2, Parallelism: 4, SaltLength: 16, KeyLength: 32}, // wrong parallelism
		{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 8, KeyLength: 32},  // short salt
		{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 16}, // short key
	}
	for i, p := range weak {
		h, err := HashPasswordWithParams("password-123", p)
		if err != nil {
			t.Fatalf("case %d hash: %v", i, err)
		}
		ok, rehash, err := VerifyPassword(h, "password-123")
		if err != nil {
			t.Fatalf("case %d verify: %v", i, err)
		}
		if !ok {
			t.Fatalf("case %d: password must still verify", i)
		}
		if !rehash {
			t.Fatalf("case %d: weak parameters must request a rehash", i)
		}
	}
}

// --- dummy verification ----------------------------------------------------

func TestDummyVerifyDoesNotPanicAndAlwaysFails(t *testing.T) {
	if h := dummyHash(); !IsArgon2idHash(h) {
		t.Fatalf("dummy hash must be argon2id, got %q", h)
	}
	DummyVerify("anything")
	// The dummy hash must not accidentally accept a common password.
	ok, _, err := VerifyPassword(dummyHash(), "password")
	if err != nil {
		t.Fatalf("verify dummy: %v", err)
	}
	if ok {
		t.Fatal("dummy hash must never validate a real password")
	}
}
