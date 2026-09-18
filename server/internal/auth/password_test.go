package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testParams keep argon2 honest but cheap. Production cost is 64 MiB; running
// that per assertion would make the suite take minutes for no extra coverage.
var testParams = Params{Memory: 8 * 1024, Time: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

func TestHasherRoundTrip(t *testing.T) {
	t.Parallel()
	h := NewHasher(testParams)

	tests := []struct {
		name     string
		password string
	}{
		{"ascii", "correct-horse-battery"},
		{"cyrillic", "пароль-на-русском-языке"},
		{"emoji", "🥔🥕 борщ 2026"},
		{"long", strings.Repeat("a", 200)},
		{"spaces", "   leading and trailing   "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := h.Hash(tc.password)
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(encoded, "$argon2id$v=19$"),
				"hash must be PHC-encoded, got %q", encoded)
			require.NotContains(t, encoded, tc.password,
				"the plaintext must not survive into the stored hash")

			require.NoError(t, h.Verify(encoded, tc.password))
			require.ErrorIs(t, h.Verify(encoded, tc.password+"x"), ErrPasswordMismatch)
		})
	}
}

func TestHashUsesFreshSalt(t *testing.T) {
	t.Parallel()
	h := NewHasher(testParams)

	first, err := h.Hash("same-password")
	require.NoError(t, err)
	second, err := h.Hash("same-password")
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "equal passwords must not produce equal hashes")
	require.NoError(t, h.Verify(first, "same-password"))
	require.NoError(t, h.Verify(second, "same-password"))
}

func TestHashEncodesParams(t *testing.T) {
	t.Parallel()
	h := NewHasher(Params{Memory: 16 * 1024, Time: 2, Parallelism: 3, SaltLen: 16, KeyLen: 32})

	encoded, err := h.Hash("whatever")
	require.NoError(t, err)
	require.Contains(t, encoded, "$m=16384,t=2,p=3$")

	params, salt, key, err := decodeHash(encoded)
	require.NoError(t, err)
	assert.Equal(t, uint32(16*1024), params.Memory)
	assert.Equal(t, uint32(2), params.Time)
	assert.Equal(t, uint8(3), params.Parallelism)
	assert.Len(t, salt, 16)
	assert.Len(t, key, 32)
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	t.Parallel()
	h := NewHasher(testParams)

	tests := []struct {
		name    string
		encoded string
		wantErr error
	}{
		{"empty", "", ErrInvalidHash},
		{"not phc", "plaintext-password", ErrInvalidHash},
		{"too few segments", "$argon2id$v=19$m=8192,t=1,p=1$c2FsdA", ErrInvalidHash},
		{"wrong algorithm", "$argon2i$v=19$m=8192,t=1,p=1$c2FsdA$aGFzaA", ErrInvalidHash},
		{"bcrypt", "$2a$10$abcdefghijklmnopqrstuv", ErrInvalidHash},
		{"bad version", "$argon2id$v=16$m=8192,t=1,p=1$c2FsdA$aGFzaA", ErrIncompatibleVersion},
		{"missing cost key", "$argon2id$v=19$m=8192,t=1$c2FsdA$aGFzaA", ErrInvalidHash},
		{"unknown cost key", "$argon2id$v=19$m=8192,t=1,x=1$c2FsdA$aGFzaA", ErrInvalidHash},
		{"zero memory", "$argon2id$v=19$m=0,t=1,p=1$c2FsdA$aGFzaA", ErrInvalidHash},
		{"zero parallelism", "$argon2id$v=19$m=8192,t=1,p=0$c2FsdA$aGFzaA", ErrInvalidHash},
		{"non-numeric cost", "$argon2id$v=19$m=lots,t=1,p=1$c2FsdA$aGFzaA", ErrInvalidHash},
		{"bad base64 salt", "$argon2id$v=19$m=8192,t=1,p=1$!!!$aGFzaA", ErrInvalidHash},
		{"empty key", "$argon2id$v=19$m=8192,t=1,p=1$c2FsdA$", ErrInvalidHash},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := h.Verify(tc.encoded, "any-password")
			require.Error(t, err)
			require.ErrorIs(t, err, tc.wantErr)
			require.NotErrorIs(t, err, ErrPasswordMismatch,
				"an unreadable hash must not look like a wrong password")
		})
	}
}

func TestNeedsRehash(t *testing.T) {
	t.Parallel()

	current := Params{Memory: 16 * 1024, Time: 2, Parallelism: 2, SaltLen: 16, KeyLen: 32}
	hasher := NewHasher(current)

	tests := []struct {
		name   string
		stored Params
		want   bool
	}{
		{"same params", current, false},
		{"weaker memory", Params{Memory: 8 * 1024, Time: 2, Parallelism: 2, SaltLen: 16, KeyLen: 32}, true},
		{"fewer passes", Params{Memory: 16 * 1024, Time: 1, Parallelism: 2, SaltLen: 16, KeyLen: 32}, true},
		{"different lanes", Params{Memory: 16 * 1024, Time: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32}, true},
		{"shorter salt", Params{Memory: 16 * 1024, Time: 2, Parallelism: 2, SaltLen: 8, KeyLen: 32}, true},
		{"shorter key", Params{Memory: 16 * 1024, Time: 2, Parallelism: 2, SaltLen: 16, KeyLen: 16}, true},
		{"stronger memory", Params{Memory: 32 * 1024, Time: 2, Parallelism: 2, SaltLen: 16, KeyLen: 32}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := NewHasher(tc.stored).Hash("upgrade-me")
			require.NoError(t, err)

			assert.Equal(t, tc.want, hasher.NeedsRehash(encoded))
			// Whatever the verdict, the old hash must still verify: a rehash is
			// an upgrade, never a lockout.
			require.NoError(t, hasher.Verify(encoded, "upgrade-me"))
		})
	}
}

func TestNeedsRehashOnUnreadableHash(t *testing.T) {
	t.Parallel()
	h := NewHasher(testParams)
	assert.True(t, h.NeedsRehash(""), "a hash we cannot parse cannot be kept")
	assert.True(t, h.NeedsRehash("$2a$10$something"))
}

func TestNewHasherFillsZeroParams(t *testing.T) {
	t.Parallel()
	got := NewHasher(Params{}).Params()
	assert.Equal(t, DefaultParams, got,
		"a zero-valued Params must not silently produce a cost of zero")
}

func TestVerifyDummyDoesNotPanic(t *testing.T) {
	t.Parallel()
	h := NewHasher(testParams)
	// The point is the work it burns, which a test cannot assert on reliably.
	// What it can assert is that the timing-equalising path is safe to call
	// repeatedly and never reports success to its caller.
	h.VerifyDummy("first")
	h.VerifyDummy("second")
	require.NotEmpty(t, h.dummy, "the dummy hash should be built on first use")
	require.NoError(t, h.Verify(h.dummy, "dummy-password-for-constant-time-login"))
}
