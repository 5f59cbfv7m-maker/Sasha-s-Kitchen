package auth

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testSecret = []byte("test-secret-at-least-32-bytes-long!!")

// fixedClock returns a controllable clock so expiry can be tested without
// sleeping through it.
type fixedClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock(t time.Time) *fixedClock { return &fixedClock{now: t} }

func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fixedClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func testUser(admin, author bool) User {
	return User{ID: uuid.New(), Handle: "tester", Status: StatusActive, IsAdmin: admin, IsAuthor: author}
}

func TestIssueAndParseAccess(t *testing.T) {
	t.Parallel()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	ts := NewTokenService(testSecret, 15*time.Minute, time.Hour, clock.Now)

	tests := []struct {
		name      string
		user      User
		wantRoles []string
	}{
		{"plain user", testUser(false, false), []string{RoleUser}},
		{"author", testUser(false, true), []string{RoleUser, RoleAuthor}},
		{"admin", testUser(true, false), []string{RoleUser, RoleAdmin}},
		{"admin author", testUser(true, true), []string{RoleUser, RoleAuthor, RoleAdmin}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			token, expires, err := ts.IssueAccess(tc.user)
			require.NoError(t, err)
			assert.Equal(t, clock.Now().Add(15*time.Minute), expires)

			claims, err := ts.ParseAccess(token)
			require.NoError(t, err)
			assert.Equal(t, tc.wantRoles, claims.Roles)
			assert.NotEmpty(t, claims.ID, "jti must be present so a session is traceable")

			sub, err := claims.SubjectID()
			require.NoError(t, err)
			assert.Equal(t, tc.user.ID, sub)
		})
	}
}

func TestParseAccessRejectsBadTokens(t *testing.T) {
	t.Parallel()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	ts := NewTokenService(testSecret, 15*time.Minute, time.Hour, clock.Now)
	user := testUser(false, false)

	valid, _, err := ts.IssueAccess(user)
	require.NoError(t, err)

	// Same claims, signed with the attacker's own key rather than ours.
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, AccessClaims{
		Roles: []string{RoleAdmin},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			Issuer:    tokenIssuer,
			ExpiresAt: jwt.NewNumericDate(clock.Now().Add(time.Hour)),
		},
	}).SignedString([]byte("not-the-server-secret-but-long-enough"))
	require.NoError(t, err)

	// "alg":"none" — the classic downgrade. jwt/v5 refuses to sign it, so it is
	// assembled by hand exactly as an attacker would.
	unsignedHeader := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	unsignedBody := base64.RawURLEncoding.EncodeToString([]byte(
		`{"sub":"` + user.ID.String() + `","iss":"` + tokenIssuer + `","exp":99999999999,"roles":["admin"]}`))
	unsigned := unsignedHeader + "." + unsignedBody + "."

	// Same secret, different issuer: a token minted for a sibling service.
	foreignIssuer, err := jwt.NewWithClaims(jwt.SigningMethodHS256, AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			Issuer:    "some-other-service",
			ExpiresAt: jwt.NewNumericDate(clock.Now().Add(time.Hour)),
		},
	}).SignedString(testSecret)
	require.NoError(t, err)

	// Signed correctly but with no exp at all.
	noExpiry, err := jwt.NewWithClaims(jwt.SigningMethodHS256, AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: user.ID.String(), Issuer: tokenIssuer},
	}).SignedString(testSecret)
	require.NoError(t, err)

	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"garbage", "not-a-jwt"},
		{"truncated", strings.Split(valid, ".")[0]},
		{"tampered signature", valid[:len(valid)-4] + "AAAA"},
		{"foreign signing key", forged},
		{"alg none", unsigned},
		{"foreign issuer", foreignIssuer},
		{"no expiry", noExpiry},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ts.ParseAccess(tc.token)
			require.Error(t, err, "token %q must not be accepted", tc.name)
		})
	}
}

func TestParseAccessHonoursExpiry(t *testing.T) {
	t.Parallel()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	ts := NewTokenService(testSecret, 15*time.Minute, time.Hour, clock.Now)

	token, _, err := ts.IssueAccess(testUser(false, false))
	require.NoError(t, err)

	_, err = ts.ParseAccess(token)
	require.NoError(t, err, "token must be valid before it expires")

	clock.Advance(16 * time.Minute)
	_, err = ts.ParseAccess(token)
	require.ErrorIs(t, err, jwt.ErrTokenExpired)
}

func TestRefreshSecretEntropy(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 512)
	for i := 0; i < 512; i++ {
		raw, hash, err := newRefreshSecret()
		require.NoError(t, err)

		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(decoded), 32,
			"a refresh token is a month-long bearer credential and needs at least 32 bytes")

		require.Len(t, hash, 32)
		require.Equal(t, HashRefreshToken(raw), hash)
		require.NotEqual(t, raw, string(hash), "the stored value must not be the token itself")

		_, dup := seen[raw]
		require.False(t, dup, "refresh tokens must not repeat")
		seen[raw] = struct{}{}
	}
}

// memStore is an in-memory RefreshStore. Rotation and reuse detection are pure
// state machines, so they are tested here with no database in the loop.
type memStore struct {
	mu   sync.Mutex
	rows map[uuid.UUID]*RefreshRecord
	now  func() time.Time
}

func newMemStore(now func() time.Time) *memStore {
	return &memStore{rows: map[uuid.UUID]*RefreshRecord{}, now: now}
}

func (m *memStore) InsertRefreshToken(_ context.Context, rec RefreshRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := rec
	m.rows[rec.ID] = &copied
	return nil
}

func (m *memStore) FindRefreshToken(_ context.Context, hash []byte) (RefreshRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range m.rows {
		if string(rec.TokenHash) == string(hash) {
			return *rec, nil
		}
	}
	return RefreshRecord{}, ErrRefreshNotFound
}

func (m *memStore) RotateRefreshToken(_ context.Context, oldID uuid.UUID, next RefreshRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.rows[oldID]
	if !ok || old.Revoked() {
		return ErrRefreshReuse
	}
	old.RevokedAt = m.now()
	copied := next
	m.rows[next.ID] = &copied
	return nil
}

func (m *memStore) RevokeFamily(_ context.Context, familyID uuid.UUID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, rec := range m.rows {
		if rec.FamilyID == familyID && !rec.Revoked() {
			rec.RevokedAt = m.now()
			n++
		}
	}
	return n, nil
}

func (m *memStore) RevokeAllForUser(_ context.Context, userID uuid.UUID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, rec := range m.rows {
		if rec.UserID == userID && !rec.Revoked() {
			rec.RevokedAt = m.now()
			n++
		}
	}
	return n, nil
}

func (m *memStore) liveCount(userID uuid.UUID) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, rec := range m.rows {
		if rec.UserID == userID && !rec.Revoked() {
			n++
		}
	}
	return n
}

func (m *memStore) get(id uuid.UUID) RefreshRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return *m.rows[id]
}

var _ RefreshStore = (*memStore)(nil)

func TestRotatorIssueAndRedeem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	store := newMemStore(clock.Now)
	rot := NewRotator(store, 24*time.Hour, clock.Now)
	userID := uuid.New()

	raw, first, err := rot.Issue(ctx, userID, SessionMeta{UserAgent: "test", IP: "127.0.0.1"})
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, first.ParentID, "the first token of a family has no parent")
	assert.Equal(t, clock.Now().Add(24*time.Hour), first.ExpiresAt)

	clock.Advance(time.Minute)
	nextRaw, second, err := rot.Redeem(ctx, raw, SessionMeta{UserAgent: "test"})
	require.NoError(t, err)

	assert.NotEqual(t, raw, nextRaw, "rotation must hand out a new secret")
	assert.Equal(t, first.FamilyID, second.FamilyID, "rotation stays inside the family")
	assert.Equal(t, first.ID, second.ParentID, "the chain records where it came from")
	assert.True(t, store.get(first.ID).Revoked(), "the redeemed token must be retired")
	assert.False(t, store.get(second.ID).Revoked())
	assert.Equal(t, 1, store.liveCount(userID), "exactly one token stays live per family")
}

func TestRotatorDetectsReuseAndRevokesOnlyThatFamily(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	store := newMemStore(clock.Now)
	rot := NewRotator(store, 24*time.Hour, clock.Now)
	userID := uuid.New()

	// Two devices: two families for the same user.
	phoneRaw, phone, err := rot.Issue(ctx, userID, SessionMeta{UserAgent: "iPhone"})
	require.NoError(t, err)
	_, tablet, err := rot.Issue(ctx, userID, SessionMeta{UserAgent: "iPad"})
	require.NoError(t, err)
	require.NotEqual(t, phone.FamilyID, tablet.FamilyID)

	// The phone rotates normally.
	rotatedRaw, rotated, err := rot.Redeem(ctx, phoneRaw, SessionMeta{UserAgent: "iPhone"})
	require.NoError(t, err)

	// A thief replays the stolen, already-retired token.
	_, _, err = rot.Redeem(ctx, phoneRaw, SessionMeta{UserAgent: "thief"})
	require.ErrorIs(t, err, ErrRefreshReuse)

	assert.True(t, store.get(phone.ID).Revoked())
	assert.True(t, store.get(rotated.ID).Revoked(),
		"the live token of a compromised chain must die with it")
	assert.False(t, store.get(tablet.ID).Revoked(),
		"the other device must survive: one leaked chain is not a reason to sign the user out everywhere")
	assert.Equal(t, 1, store.liveCount(userID))

	// The honest client's newest token is dead too — both parties re-login.
	_, _, err = rot.Redeem(ctx, rotatedRaw, SessionMeta{})
	require.ErrorIs(t, err, ErrRefreshReuse)
}

func TestRotatorRedeemFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func(t *testing.T, rot *Rotator, clock *fixedClock, store *memStore, userID uuid.UUID) string
		wantErr error
	}{
		{
			name: "unknown token",
			setup: func(t *testing.T, _ *Rotator, _ *fixedClock, _ *memStore, _ uuid.UUID) string {
				raw, _, err := newRefreshSecret()
				require.NoError(t, err)
				return raw
			},
			wantErr: ErrRefreshInvalid,
		},
		{
			name: "empty token",
			setup: func(_ *testing.T, _ *Rotator, _ *fixedClock, _ *memStore, _ uuid.UUID) string {
				return ""
			},
			wantErr: ErrRefreshInvalid,
		},
		{
			name: "expired token",
			setup: func(t *testing.T, rot *Rotator, clock *fixedClock, _ *memStore, userID uuid.UUID) string {
				raw, _, err := rot.Issue(ctx, userID, SessionMeta{})
				require.NoError(t, err)
				clock.Advance(25 * time.Hour)
				return raw
			},
			wantErr: ErrRefreshExpired,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
			store := newMemStore(clock.Now)
			rot := NewRotator(store, 24*time.Hour, clock.Now)
			userID := uuid.New()

			raw := tc.setup(t, rot, clock, store, userID)
			_, _, err := rot.Redeem(ctx, raw, SessionMeta{})
			require.ErrorIs(t, err, tc.wantErr)
			assert.Equal(t, 0, store.liveCount(userID))
		})
	}
}

func TestRotatorRevokeChain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	store := newMemStore(clock.Now)
	rot := NewRotator(store, 24*time.Hour, clock.Now)
	userID := uuid.New()

	phoneRaw, _, err := rot.Issue(ctx, userID, SessionMeta{UserAgent: "iPhone"})
	require.NoError(t, err)
	_, tablet, err := rot.Issue(ctx, userID, SessionMeta{UserAgent: "iPad"})
	require.NoError(t, err)

	require.NoError(t, rot.RevokeChain(ctx, phoneRaw))
	assert.False(t, store.get(tablet.ID).Revoked(), "signing out one device leaves the others alone")
	assert.Equal(t, 1, store.liveCount(userID))

	// The signed-out token can no longer be redeemed, and replaying it reads as
	// reuse — which is harmless here, the family is already gone.
	_, _, err = rot.Redeem(ctx, phoneRaw, SessionMeta{})
	require.ErrorIs(t, err, ErrRefreshReuse)

	require.ErrorIs(t, rot.RevokeChain(ctx, "never-issued"), ErrRefreshInvalid)
}

func TestRotatorConcurrentRedeemHasOneWinner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	store := newMemStore(clock.Now)
	rot := NewRotator(store, 24*time.Hour, clock.Now)
	userID := uuid.New()

	raw, _, err := rot.Issue(ctx, userID, SessionMeta{})
	require.NoError(t, err)

	const racers = 8
	var wg sync.WaitGroup
	results := make([]error, racers)
	wg.Add(racers)
	for i := range racers {
		go func() {
			defer wg.Done()
			_, _, results[i] = rot.Redeem(ctx, raw, SessionMeta{})
		}()
	}
	wg.Wait()

	succeeded := 0
	for _, err := range results {
		if err == nil {
			succeeded++
		}
	}
	assert.Equal(t, 1, succeeded,
		"a single refresh token must be redeemable exactly once, even under a race")
}
