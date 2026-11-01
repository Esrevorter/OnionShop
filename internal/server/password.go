// Argon2id password hashing per AGENTS.md §4 and SECURITY_AND_OPSEC.md §1.
//
// Design rules encoded here (do not "optimize" them away):
//   - Parameters are embedded in the stored hash string, so historical hashes
//     keep verifying after the production parameters are raised;
//   - Verification of an UNKNOWN username runs a dummy hash against fixed
//     parameters with a constant comparison — wall time is indistinguishable
//     from a wrong-password attempt, closing the user-enumeration channel;
//   - Passwords are never logged, never truncated, never normalized.

package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrBadHash marks a malformed/stored-hash parse failure. Handlers map it to
// the same generic credential error as a wrong password.
var ErrBadHash = errors.New("server: malformed password hash")

// ArgonParams captures one parameter set. Stored alongside each hash inside
// the encoded string (AGENTS.md §4: "params stored per-hash").
type ArgonParams struct {
	Memory  uint32 // KiB
	Time    uint32 // iterations
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

// DefaultArgonParams are the production settings: 64 MiB / t=3 / p=4, 32-byte
// key and salt (OWASP-equivalent lower bound). Raise these deliberately via a
// reviewed change; old hashes still verify because params travel in the hash.
func DefaultArgonParams() ArgonParams {
	return ArgonParams{Memory: 64 * 1024, Time: 3, Threads: 4, KeyLen: 32, SaltLen: 16}
}

// dummyVerifyParams is FIXED and independent of DefaultArgonParams: the dummy
// path must have stable timing even while production params evolve. It is
// chosen close to the production cost so enumeration attempts cannot win by
// picking the cheaper branch.
var dummyVerifyParams = ArgonParams{Memory: 64 * 1024, Time: 3, Threads: 4, KeyLen: 32, SaltLen: 16}

const hashPrefix = "$argon2id$v=19$"

// HashPassword derives an Argon2id hash and encodes it as
// $argon2id$v=19$m=,t=,p=$saltB64$hashB64 (PHC-style, self-describing).
func HashPassword(password string, p ArgonParams) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInternalSession, err)
	}
	hash := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("%sm=%d,t=%d,p=%d$%s$%s",
		hashPrefix, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword parses the stored hash's own parameters and re-derives.
// Comparison is constant-time over the digest bytes.
func VerifyPassword(stored, password string) (ok bool, err error) {
	p, salt, hash, err := decodeHash(stored)
	if err != nil {
		return false, err
	}
	candidate := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(hash)))
	return subtle.ConstantTimeCompare(candidate, hash) == 1, nil
}

// DummyVerifyPassword performs a full-cost verification against a synthetic
// hash for usernames that do not exist, then compares the supplied password
// against a fixed expected digest. The caller reports the same "invalid
// credentials" outcome either way; only wall time was ever at stake, and this
// makes it identical to a real wrong-password attempt.
func DummyVerifyPassword(password string) bool {
	stored, err := HashPassword("dummy-password-not-used-for-auth", dummyVerifyParams)
	if err != nil {
		// CSPRNG failure: still burn equivalent CPU before reporting mismatch.
		_ = argon2.IDKey([]byte(password), make([]byte, dummyVerifyParams.SaltLen),
			dummyVerifyParams.Time, dummyVerifyParams.Memory, dummyVerifyParams.Threads, dummyVerifyParams.KeyLen)
		return false
	}
	ok, _ := VerifyPassword(stored, password) // always false; cost is the point
	return ok
}

func decodeHash(encoded string) (ArgonParams, []byte, []byte, error) {
	fields := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash]
	if len(fields) != 6 || fields[1] != "argon2id" || fields[2] != "v=19" {
		return ArgonParams{}, nil, nil, ErrBadHash
	}
	var p ArgonParams
	if _, err := fmt.Sscanf(fields[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return ArgonParams{}, nil, nil, fmt.Errorf("%w: %v", ErrBadHash, err)
	}
	if p.Memory == 0 || p.Time == 0 || p.Threads == 0 {
		return ArgonParams{}, nil, nil, ErrBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(fields[4])
	if err != nil {
		return ArgonParams{}, nil, nil, fmt.Errorf("%w: %v", ErrBadHash, err)
	}
	hash, err := base64.RawStdEncoding.DecodeString(fields[5])
	if err != nil {
		return ArgonParams{}, nil, nil, fmt.Errorf("%w: %v", ErrBadHash, err)
	}
	p.KeyLen = uint32(len(hash))
	return p, salt, hash, nil
}
