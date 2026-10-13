package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// Password hashing policy.
//
// New passwords are always stored as Argon2id in the PHC string format. The
// parameters below follow the OWASP Password Storage Cheat Sheet recommendation
// for Argon2id (19 MiB, t=2, p=1). Hashes written by older releases used
// bcrypt; VerifyPassword still understands them so an existing deployment can
// migrate without forcing a password reset. The caller re-hashes on the next
// successful login when NeedsRehash reports true.
const (
	argon2idPrefix = "$argon2id$"
	argon2idName   = "argon2id"
	argon2idVer    = argon2.Version // 19

	defaultMemoryKiB   uint32 = 19 * 1024
	defaultIterations  uint32 = 2
	defaultParallelism uint8  = 1
	defaultSaltLength  uint32 = 16
	defaultKeyLength   uint32 = 32
)

// Hard bounds on the parameters read back from a stored PHC string.
//
// The verifier derives a key with whatever the row says, so a corrupted or
// tampered row (m=4294967295, t=999999, p=255) would turn one login attempt
// into a CPU and memory denial of service. Anything outside these ranges is
// rejected before argon2.IDKey is called, so the allocation never happens.
//
// The ceilings are far above the current policy (19 MiB / t=2 / p=1) yet far
// below anything that could exhaust a gateway node, which leaves room for a
// future policy bump without a code change.
const (
	minArgon2MemoryKiB   uint32 = 8 * 1024   // 8 MiB
	maxArgon2MemoryKiB   uint32 = 256 * 1024 // 256 MiB
	minArgon2Iterations  uint32 = 1
	maxArgon2Iterations  uint32 = 10
	minArgon2Parallelism uint8  = 1
	maxArgon2Parallelism uint8  = 4
	minArgon2SaltBytes   uint32 = 8
	maxArgon2SaltBytes   uint32 = 64
	minArgon2KeyBytes    uint32 = 16
	maxArgon2KeyBytes    uint32 = 64
)

// validateArgon2Params enforces the bounds above. It is called after parsing
// and before any key derivation.
func validateArgon2Params(p PasswordParams, saltLen, keyLen uint32) error {
	if p.Memory < minArgon2MemoryKiB || p.Memory > maxArgon2MemoryKiB {
		return ErrPasswordHashMalformed
	}
	if p.Iterations < minArgon2Iterations || p.Iterations > maxArgon2Iterations {
		return ErrPasswordHashMalformed
	}
	if p.Parallelism < minArgon2Parallelism || p.Parallelism > maxArgon2Parallelism {
		return ErrPasswordHashMalformed
	}
	if saltLen < minArgon2SaltBytes || saltLen > maxArgon2SaltBytes {
		return ErrPasswordHashMalformed
	}
	if keyLen < minArgon2KeyBytes || keyLen > maxArgon2KeyBytes {
		return ErrPasswordHashMalformed
	}
	return nil
}

// Sentinel errors. Callers match on these instead of inspecting error strings.
var (
	// ErrPasswordHashMalformed means the stored value is not a hash we wrote:
	// missing fields, bad base64, or unparsable parameters.
	ErrPasswordHashMalformed = errors.New("password hash is malformed")
	// ErrPasswordHashUnsupported means the stored value uses an algorithm this
	// build does not know how to verify.
	ErrPasswordHashUnsupported = errors.New("password hash algorithm is not supported")
	// ErrPasswordEmpty is returned when hashing an empty password. Callers
	// should reject short passwords before reaching this layer.
	ErrPasswordEmpty = errors.New("password must not be empty")
)

// PasswordParams describes the Argon2id work factors used for a hash.
type PasswordParams struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultPasswordParams returns the current policy. Callers that need a cheaper
// profile (tests) can pass their own values to HashPasswordWithParams.
func DefaultPasswordParams() PasswordParams {
	return PasswordParams{
		Memory:      defaultMemoryKiB,
		Iterations:  defaultIterations,
		Parallelism: defaultParallelism,
		SaltLength:  defaultSaltLength,
		KeyLength:   defaultKeyLength,
	}
}

// HashPassword derives an Argon2id hash with the default policy and returns it
// in PHC format: $argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>.
func HashPassword(plain string) (string, error) {
	return HashPasswordWithParams(plain, DefaultPasswordParams())
}

// HashPasswordWithParams is HashPassword with explicit work factors. A fresh
// random salt is generated for every call, so hashing the same password twice
// yields different strings.
func HashPasswordWithParams(plain string, p PasswordParams) (string, error) {
	if plain == "" {
		return "", ErrPasswordEmpty
	}
	if p.SaltLength == 0 {
		p.SaltLength = defaultSaltLength
	}
	if p.KeyLength == 0 {
		p.KeyLength = defaultKeyLength
	}
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	return fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2idName, argon2idVer, p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether plain matches the stored hash.
//
// A wrong password is not an error: it returns (false, needsRehash, nil). An
// error is only returned when the stored hash itself cannot be understood,
// which signals data corruption rather than a bad login attempt.
//
// needsRehash is true when the hash is valid but was produced with a different
// algorithm or weaker parameters than the current policy. Callers should
// re-hash and persist the password on the next successful login.
func VerifyPassword(encoded, plain string) (valid bool, needsRehash bool, err error) {
	if encoded == "" {
		return false, false, ErrPasswordHashMalformed
	}
	switch {
	case strings.HasPrefix(encoded, argon2idPrefix):
		valid, err = verifyArgon2id(encoded, plain)
		if err != nil {
			return false, false, err
		}
		return valid, valid && argon2idNeedsRehash(encoded), nil
	case isBcryptHash(encoded):
		err = bcrypt.CompareHashAndPassword([]byte(encoded), []byte(plain))
		if err == nil {
			// Legacy algorithm: always migrate to Argon2id on next login.
			return true, true, nil
		}
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, false, nil
		}
		// A malformed bcrypt hash (bad cost, wrong length) lands here.
		return false, false, ErrPasswordHashMalformed
	default:
		return false, false, ErrPasswordHashUnsupported
	}
}

// verifyArgon2id parses a PHC string and compares in constant time.
func verifyArgon2id(encoded, plain string) (bool, error) {
	p, salt, want, err := decodeArgon2id(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(plain), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// decodeArgon2id splits $argon2id$v=19$m=..,t=..,p=..$salt$key and validates
// every field before it is used as an Argon2 parameter.
func decodeArgon2id(encoded string) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// Leading empty segment from the leading '$' plus five fields.
	if len(parts) != 6 || parts[0] != "" {
		return PasswordParams{}, nil, nil, ErrPasswordHashMalformed
	}
	if parts[1] != argon2idName {
		return PasswordParams{}, nil, nil, ErrPasswordHashUnsupported
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return PasswordParams{}, nil, nil, ErrPasswordHashMalformed
	}
	if version != argon2idVer {
		return PasswordParams{}, nil, nil, ErrPasswordHashUnsupported
	}

	var p PasswordParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return PasswordParams{}, nil, nil, ErrPasswordHashMalformed
	}
	if p.Memory == 0 || p.Iterations == 0 || p.Parallelism == 0 {
		return PasswordParams{}, nil, nil, ErrPasswordHashMalformed
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return PasswordParams{}, nil, nil, ErrPasswordHashMalformed
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return PasswordParams{}, nil, nil, ErrPasswordHashMalformed
	}
	// Reject absurd work factors before they reach argon2.IDKey, which would
	// otherwise allocate memory and burn CPU as instructed by the row.
	if err := validateArgon2Params(p, uint32(len(salt)), uint32(len(key))); err != nil {
		return PasswordParams{}, nil, nil, err
	}
	return p, salt, key, nil
}

// argon2idNeedsRehash reports whether an existing Argon2id hash was produced
// with parameters weaker than the current policy.
func argon2idNeedsRehash(encoded string) bool {
	p, salt, key, err := decodeArgon2id(encoded)
	if err != nil {
		return true
	}
	d := DefaultPasswordParams()
	if p.Memory != d.Memory || p.Iterations != d.Iterations || p.Parallelism != d.Parallelism {
		return true
	}
	if uint32(len(salt)) != d.SaltLength || uint32(len(key)) != d.KeyLength {
		return true
	}
	return false
}

// isBcryptHash recognises the three bcrypt variants seen in the wild. The cost
// and salt are validated by bcrypt itself during comparison.
func isBcryptHash(encoded string) bool {
	return strings.HasPrefix(encoded, "$2a$") ||
		strings.HasPrefix(encoded, "$2b$") ||
		strings.HasPrefix(encoded, "$2y$")
}

// IsBcryptHash reports whether a stored hash uses the legacy bcrypt format.
// Exposed for migration tooling and tests.
func IsBcryptHash(encoded string) bool { return isBcryptHash(encoded) }

// IsArgon2idHash reports whether a stored hash uses Argon2id.
func IsArgon2idHash(encoded string) bool { return strings.HasPrefix(encoded, argon2idPrefix) }

// dummyHash is a valid Argon2id hash of a random value, computed once. Login
// runs it when the username does not exist so a failed lookup costs the same
// wall-clock time as a wrong password, which blunts username enumeration.
var dummyHash = sync.OnceValue(func() string {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		// Fall back to a fixed value; timing equalisation degrades but login
		// must keep working.
		return "$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	h, err := HashPassword(base64.RawStdEncoding.EncodeToString(secret))
	if err != nil {
		return "$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	return h
})

// DummyVerify burns roughly the same CPU as a real verification. It always
// fails; the result is deliberately discarded by callers.
func DummyVerify(plain string) {
	_, _, _ = VerifyPassword(dummyHash(), plain)
}
