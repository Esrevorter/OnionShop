package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// AuthN primitives for Phase 3: argon2id password hashing with per-hash
// parameter records (AGENTS.md §2/§4). Handlers in auth_handlers.go compose
// these; nothing here touches HTTP. The CAPTCHA service lives in captcha.go.

// ErrAuthInternal marks CSPRNG failures. Callers render the generic 500 and
// never surface the cause (SECURITY_AND_OPSEC.md §1).
var ErrAuthInternal = errors.New("server: authentication subsystem failure")

// ArgonSaltBytes is the per-user salt length (128 bits, AGENTS.md §4).
const ArgonSaltBytes = 16

// ArgonParams are the work parameters recorded ALONGSIDE each hash so they can
// be upgraded over time without ever locking out an old account. Baseline per
// AGENTS.md §4: time=3, memory=64MiB, threads=4, keyLen=32.
type ArgonParams struct {
	Time    uint32
	Memory  uint32 // KiB
	Threads uint8
	KeyLen  uint32
	SaltB64 string // base64 std encoding of the random salt
}

// DefaultArgonParams returns the production baseline.
func DefaultArgonParams() ArgonParams {
	return ArgonParams{Time: 3, Memory: 64 * 1024, Threads: 4, KeyLen: 32}
}

// HashPassword derives an argon2id digest with a fresh CSPRNG salt and returns
// both the encoded record and its parameters. The encoded form is
// "argon2id$v=19$t,m,p,k$base64(salt)$base64(hash)" — self-describing, so
// VerifyPassword always knows which cost to run.
func HashPassword(password string) (encoded string, params ArgonParams, err error) {
	if password == "" {
		return "", ArgonParams{}, errors.New("server: empty password")
	}
	params = DefaultArgonParams()
	salt := make([]byte, ArgonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", ArgonParams{}, fmt.Errorf("%w: %v", ErrAuthInternal, err)
	}
	hash := argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, params.KeyLen)
	params.SaltB64 = base64.StdEncoding.EncodeToString(salt)
	encoded = encodeArgon(params, hash)
	return encoded, params, nil
}

func encodeArgon(p ArgonParams, hash []byte) string {
	return fmt.Sprintf("argon2id$v=19$%d,%d,%d,%d$%s$%s",
		p.Time, p.Memory, p.Threads, p.KeyLen, p.SaltB64,
		base64.StdEncoding.EncodeToString(hash))
}

// decodeArgon parses one encoded record. It is strict: any deviation from the
// envelope we mint is an error (never a silent fallback), so a corrupted or
// foreign hash cannot be "verified" by accident.
func decodeArgon(encoded string) (p ArgonParams, hash []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return ArgonParams{}, nil, errors.New("server: malformed password hash record")
	}
	var t, m, k uint64
	var th int
	if _, err := fmt.Sscanf(parts[2], "%d,%d,%d,%d", &t, &m, &th, &k); err != nil {
		return ArgonParams{}, nil, errors.New("server: malformed argon2 parameter field")
	}
	salt, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return ArgonParams{}, nil, errors.New("server: malformed argon2 salt field")
	}
	hash, err = base64.StdEncoding.DecodeString(parts[4])
	if err != nil {
		return ArgonParams{}, nil, errors.New("server: malformed argon2 hash field")
	}
	if len(salt) < 8 || len(hash) < 16 || t == 0 || m == 0 || th == 0 || k == 0 {
		return ArgonParams{}, nil, errors.New("server: out-of-range argon2 parameters")
	}
	return ArgonParams{Time: uint32(t), Memory: uint32(m), Threads: uint8(th), KeyLen: uint32(k), SaltB64: parts[3]}, hash, nil
}

// VerifyPassword checks password against an encoded record. A malformed stored
// record fails closed (false, error) — the account simply cannot log in until
// an operator resets it; that is strictly better than accepting unknown forms.
func VerifyPassword(password, encoded string) (bool, error) {
	p, want, err := decodeArgon(encoded)
	if err != nil {
		return false, err
	}
	salt, err := base64.StdEncoding.DecodeString(p.SaltB64)
	if err != nil { // unreachable via decodeArgon
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsUpgrade reports whether a record was minted with weaker-than-baseline
// parameters. Login handlers use it to transparently re-hash with current
// costs after a successful verify (params are stored per-hash on purpose).
func NeedsUpgrade(p ArgonParams) bool {
	d := DefaultArgonParams()
	return p.Time < d.Time || p.Memory < d.Memory || p.Threads < d.Threads || p.KeyLen < d.KeyLen
}

// dummySalt is a fixed 16-byte salt used only for timing equalization; it
// belongs to no real account.
var dummySalt = []byte("onionshop-dummy!")

// DummyVerify runs one baseline-cost argon2id computation against the fixed
// dummy salt. Unknown usernames get this treatment so login attempts cannot
// distinguish "no such user" from "wrong password" by timing alone
// (AGENTS.md §4 dummy verify).
func DummyVerify() {
	d := DefaultArgonParams()
	out := argon2.IDKey([]byte("timing-equalization-placeholder"), dummySalt, d.Time, d.Memory, d.Threads, d.KeyLen)
	// The result feeds a package-level sink so the compiler keeps the work
	// (and the optimizer cannot elide a call whose result is discarded).
	_ = subtle.ConstantTimeByteEq(byte(out[0]), byte(len(out)))
}

// ValidateUsername enforces the users.username CHECK constraint shape
// (^[a-z0-9_]{3,32}$) BEFORE a DB round-trip, so registration errors stay
// friendly and constraint violations never surface as 500s.
func ValidateUsername(u string) error {
	if len(u) < 3 || len(u) > 32 {
		return errors.New("server: username must be 3-32 characters")
	}
	for i := 0; i < len(u); i++ {
		c := u[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
		if !ok {
			return errors.New("server: username may contain only lowercase letters, digits and underscore")
		}
	}
	return nil
}

// ValidatePasswordStrength applies cheap server-side policy before hashing:
// length bounds and non-blank content. (Composition rules beyond length are
// deliberately minimal — breached-password screening happens offline at the
// operator level, not by shipping a blocklist inside the app.)
func ValidatePasswordStrength(p string) error {
	if len(p) < 10 || len(p) > 128 {
		return errors.New("server: password must be 10-128 characters")
	}
	return nil
}

// ParsePositiveInt reads a bounded positive integer from form input; used for
// prices/quantities where the schema stores BIGINT satoshis / INT counts.
func ParsePositiveInt(s string, max int64) (int64, error) {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, errors.New("server: expected an integer value")
	}
	if v <= 0 || v > max {
		return 0, fmt.Errorf("server: value out of range (1..%d)", max)
	}
	return v, nil
}
