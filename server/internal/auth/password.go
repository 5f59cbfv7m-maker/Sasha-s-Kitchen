// Package auth owns everything about who the caller is: password storage,
// access and refresh tokens, Sign in with Apple, and the middleware that turns
// a bearer token into an identity.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Argon2 parameter errors. They describe a stored hash we cannot read, which is
// an operational problem, never something a client caused.
var (
	// ErrInvalidHash means the stored string is not a hash we can parse.
	ErrInvalidHash = errors.New("auth: malformed password hash")
	// ErrIncompatibleVersion means the hash was produced by an argon2 version
	// this build cannot verify.
	ErrIncompatibleVersion = errors.New("auth: incompatible argon2 version")
	// ErrPasswordMismatch means the password does not match the hash. This is
	// the only one of these that is an expected, routine outcome.
	ErrPasswordMismatch = errors.New("auth: password mismatch")
)

// Params are the argon2id cost parameters. They are encoded into every stored
// hash, so raising them here does not invalidate existing passwords: old hashes
// keep verifying with their own parameters and NeedsRehash flags them for a
// transparent upgrade at the next successful login.
type Params struct {
	// Memory is the memory cost in KiB. This is the parameter that actually
	// buys GPU resistance, so it is the one to raise first.
	Memory uint32
	// Time is the number of passes over memory.
	Time uint32
	// Parallelism is the number of lanes.
	Parallelism uint8
	// SaltLen and KeyLen are in bytes.
	SaltLen uint32
	KeyLen  uint32
}

// DefaultParams follows the RFC 9106 second recommended option (64 MiB, t=3),
// which is the usual balance for an interactive login on server hardware.
var DefaultParams = Params{
	Memory:      64 * 1024,
	Time:        3,
	Parallelism: 2,
	SaltLen:     16,
	KeyLen:      32,
}

// Hasher hashes and verifies passwords with argon2id.
type Hasher struct {
	params Params

	// dummyOnce guards a throwaway hash used to keep login timing flat when no
	// user matched. It is built lazily because it costs a full argon2 run.
	dummyOnce sync.Once
	dummy     string
}

// NewHasher returns a Hasher using the given cost parameters. Zero-valued
// fields fall back to DefaultParams so a partially filled struct cannot
// silently produce a worthless hash.
func NewHasher(p Params) *Hasher {
	if p.Memory == 0 {
		p.Memory = DefaultParams.Memory
	}
	if p.Time == 0 {
		p.Time = DefaultParams.Time
	}
	if p.Parallelism == 0 {
		p.Parallelism = DefaultParams.Parallelism
	}
	if p.SaltLen == 0 {
		p.SaltLen = DefaultParams.SaltLen
	}
	if p.KeyLen == 0 {
		p.KeyLen = DefaultParams.KeyLen
	}
	return &Hasher{params: p}
}

// Params returns the cost parameters this hasher writes into new hashes.
func (h *Hasher) Params() Params { return h.params }

// Hash derives a new argon2id hash with a fresh random salt and returns it in
// PHC string format: $argon2id$v=19$m=...,t=...,p=...$salt$hash.
func (h *Hasher) Hash(password string) (string, error) {
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt,
		h.params.Time, h.params.Memory, h.params.Parallelism, h.params.KeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.params.Memory, h.params.Time, h.params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify reports whether password matches encoded. It returns nil on a match,
// ErrPasswordMismatch on a clean mismatch, and a parse error when the stored
// value is not a hash we understand.
//
// The comparison is constant time: a timing signal on the digest would let an
// attacker walk the correct value out byte by byte.
func (h *Hasher) Verify(encoded, password string) error {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(password), salt,
		params.Time, params.Memory, params.Parallelism, uint32(len(want)))

	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

// NeedsRehash reports whether encoded was produced with weaker settings than
// this hasher now uses, so the caller can silently upgrade it while it still
// holds the plaintext. An unparseable hash also needs a rehash — it cannot
// verify anything as it stands.
func (h *Hasher) NeedsRehash(encoded string) bool {
	params, salt, key, err := decodeHash(encoded)
	if err != nil {
		return true
	}
	return params.Memory < h.params.Memory ||
		params.Time < h.params.Time ||
		params.Parallelism != h.params.Parallelism ||
		uint32(len(salt)) < h.params.SaltLen ||
		uint32(len(key)) < h.params.KeyLen
}

// VerifyDummy burns the same work Verify would, against a throwaway hash, and
// discards the answer. Login calls it when no account matched so that an
// unknown handle and a wrong password take indistinguishable time — otherwise
// the response latency alone enumerates which accounts exist.
func (h *Hasher) VerifyDummy(password string) {
	h.dummyOnce.Do(func() {
		// The password here is irrelevant; only the cost of hashing it matters.
		encoded, err := h.Hash("dummy-password-for-constant-time-login")
		if err != nil {
			return
		}
		h.dummy = encoded
	})
	if h.dummy == "" {
		return
	}
	_ = h.Verify(h.dummy, password)
}

// decodeHash parses a PHC argon2id string back into its parts.
func decodeHash(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// A well-formed value splits into ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, key].
	if len(parts) != 6 || parts[0] != "" {
		return Params{}, nil, nil, ErrInvalidHash
	}
	if parts[1] != "argon2id" {
		return Params{}, nil, nil, fmt.Errorf("%w: algorithm %q", ErrInvalidHash, parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return Params{}, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return Params{}, nil, nil, fmt.Errorf("%w: v=%d", ErrIncompatibleVersion, version)
	}

	params, err := parseCostParams(parts[3])
	if err != nil {
		return Params{}, nil, nil, err
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return Params{}, nil, nil, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return Params{}, nil, nil, ErrInvalidHash
	}

	params.SaltLen = uint32(len(salt))
	params.KeyLen = uint32(len(key))
	return params, salt, key, nil
}

// parseCostParams reads the "m=65536,t=3,p=2" segment. It is written by hand
// rather than with Sscanf so that a missing or duplicated key is rejected
// instead of leaving a zero cost in place.
func parseCostParams(segment string) (Params, error) {
	errBad := fmt.Errorf("%w: cost params %q", ErrInvalidHash, segment)

	fields := strings.Split(segment, ",")
	if len(fields) != 3 {
		return Params{}, errBad
	}

	var p Params
	var seenM, seenT, seenP bool
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return Params{}, errBad
		}
		numeric, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return Params{}, errBad
		}
		switch key {
		case "m":
			p.Memory, seenM = uint32(numeric), true
		case "t":
			p.Time, seenT = uint32(numeric), true
		case "p":
			if numeric == 0 || numeric > 255 {
				return Params{}, errBad
			}
			p.Parallelism, seenP = uint8(numeric), true
		default:
			return Params{}, errBad
		}
	}
	if !seenM || !seenT || !seenP || p.Memory == 0 || p.Time == 0 {
		return Params{}, errBad
	}
	return p, nil
}
