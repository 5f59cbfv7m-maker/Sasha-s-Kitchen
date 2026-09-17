package auth

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/config"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/postgres"
)

// integrationPool is shared by every integration test. It is nil when
// DATABASE_URL is unset, which is what makes those tests skip instead of fail.
var integrationPool *pgxpool.Pool

func TestMain(m *testing.M) {
	if dsn := strings.TrimSpace(os.Getenv("DATABASE_URL")); dsn != "" {
		pool, err := postgres.NewPool(context.Background(), config.PostgresConfig{
			DSN:              dsn,
			MaxConns:         8,
			MinConns:         1,
			MaxConnLifetime:  time.Hour,
			MaxConnIdleTime:  time.Minute,
			StatementTimeout: 5 * time.Second,
			ConnectTimeout:   5 * time.Second,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "auth tests: DATABASE_URL is set but unusable: %v\n", err)
			os.Exit(1)
		}
		integrationPool = pool
	}

	code := m.Run()
	if integrationPool != nil {
		integrationPool.Close()
	}
	os.Exit(code)
}

// requireDB skips cleanly when there is no database to talk to.
func requireDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if integrationPool == nil {
		t.Skip("DATABASE_URL is not set: skipping integration test")
	}
	return integrationPool
}

// fixture is one test's isolated slice of the database plus a wired-up stack.
type fixture struct {
	t     *testing.T
	pool  *pgxpool.Pool
	repo  *Repo
	svc   *Service
	auth  *Authenticator
	mux   *http.ServeMux
	clock *fixedClock

	// prefix namespaces every handle this test creates, so concurrent tests
	// cannot collide on the partial unique index.
	prefix string
	// extra holds ids of rows created outside the prefix convention, such as
	// accounts the Apple flow named for itself.
	extra []uuid.UUID
}

// newFixture builds the stack over the live database and registers cleanup.
func newFixture(t *testing.T, apple *AppleVerifier) *fixture {
	t.Helper()
	pool := requireDB(t)

	f := &fixture{
		t:      t,
		pool:   pool,
		repo:   NewRepo(pool),
		clock:  newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)),
		prefix: "t" + strings.ToLower(uuid.NewString()[:8]),
	}
	f.svc = NewService(f.repo, config.AuthConfig{
		JWTSecret:       testSecret,
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour,
		AppleClientID:   testAppleClientID,
		AppleTeamID:     "TEAMID1234",
	}, ServiceOptions{
		Hasher: NewHasher(testParams),
		Apple:  apple,
		Now:    f.clock.Now,
	})
	f.auth = NewAuthenticator(f.svc.Tokens(), f.repo)
	f.mux = http.NewServeMux()
	NewHandlers(f.svc, f.auth).Routes(f.mux)

	t.Cleanup(f.cleanup)
	return f
}

// handle returns a namespaced handle for this test.
func (f *fixture) handle(name string) string { return f.prefix + "_" + name }

// email returns a namespaced address for this test.
func (f *fixture) email(name string) string { return f.prefix + "." + name + "@example.test" }

// track marks a row for deletion that the handle prefix would not catch.
func (f *fixture) track(id uuid.UUID) { f.extra = append(f.extra, id) }

// cleanup removes everything this test created. Deleting the user cascades to
// refresh_tokens and external_identities, so one statement is enough.
func (f *fixture) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := f.pool.Exec(ctx, `DELETE FROM users WHERE handle LIKE $1`, f.prefix+"%"); err != nil {
		f.t.Errorf("cleanup by handle prefix: %v", err)
	}
	if len(f.extra) > 0 {
		if _, err := f.pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, f.extra); err != nil {
			f.t.Errorf("cleanup by id: %v", err)
		}
	}
}

// mustUser registers an account directly through the repository.
func (f *fixture) mustUser(ctx context.Context, name, password string) User {
	f.t.Helper()
	hash := ""
	if password != "" {
		var err error
		hash, err = NewHasher(testParams).Hash(password)
		require.NoError(f.t, err)
	}
	user, err := f.repo.CreateUser(ctx, NewUser{
		Handle:       f.handle(name),
		DisplayName:  "Тестовый повар",
		Email:        f.email(name),
		PasswordHash: hash,
	})
	require.NoError(f.t, err)
	return user
}

// ------------------------------------------------------------------ users --

func TestRepoCreateAndFindUser(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	created := f.mustUser(ctx, "alice", "correct-horse-battery")
	assert.Equal(t, StatusActive, created.Status)
	assert.False(t, created.IsAdmin)
	assert.False(t, created.IsAuthor)
	assert.NotEqual(t, uuid.Nil, created.ID)

	tests := []struct {
		name   string
		lookup func() (User, error)
	}{
		{"by id", func() (User, error) { return f.repo.FindByID(ctx, created.ID) }},
		{"by handle", func() (User, error) { return f.repo.FindByHandle(ctx, f.handle("alice")) }},
		{"by handle uppercase", func() (User, error) {
			return f.repo.FindByHandle(ctx, strings.ToUpper(f.handle("alice")))
		}},
		{"by email", func() (User, error) { return f.repo.FindByEmail(ctx, f.email("alice")) }},
		{"by email uppercase", func() (User, error) {
			return f.repo.FindByEmail(ctx, strings.ToUpper(f.email("alice")))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.lookup()
			require.NoError(t, err, "citext columns must match case-insensitively")
			assert.Equal(t, created.ID, got.ID)
		})
	}

	_, err := f.repo.FindByHandle(ctx, f.handle("nobody"))
	require.ErrorIs(t, err, ErrUserNotFound)
}

func TestRepoRejectsDuplicates(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	f.mustUser(ctx, "alice", "correct-horse-battery")

	tests := []struct {
		name    string
		user    NewUser
		wantMsg string
	}{
		{
			name:    "same handle",
			user:    NewUser{Handle: f.handle("alice"), DisplayName: "Другой", Email: f.email("other")},
			wantMsg: "Этот логин уже занят",
		},
		{
			name:    "same handle in another case",
			user:    NewUser{Handle: strings.ToUpper(f.handle("alice")), DisplayName: "Другой", Email: f.email("other2")},
			wantMsg: "Этот логин уже занят",
		},
		{
			name:    "same email",
			user:    NewUser{Handle: f.handle("bob"), DisplayName: "Другой", Email: f.email("alice")},
			wantMsg: "Этот e-mail уже зарегистрирован",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.repo.CreateUser(ctx, tc.user)
			require.Error(t, err)

			apiErr := httpx.AsError(err)
			assert.Equal(t, http.StatusConflict, apiErr.Status())
			assert.Equal(t, tc.wantMsg, apiErr.Message)
		})
	}
}

func TestRepoSoftDeleteFreesHandleAndEmail(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	original := f.mustUser(ctx, "alice", "correct-horse-battery")
	require.NoError(t, f.repo.SoftDeleteUser(ctx, original.ID))

	// Gone from every lookup the application makes.
	_, err := f.repo.FindByID(ctx, original.ID)
	require.ErrorIs(t, err, ErrUserNotFound)
	_, err = f.repo.FindByHandle(ctx, f.handle("alice"))
	require.ErrorIs(t, err, ErrUserNotFound)

	// The row survives, scrubbed. handle stays because it is NOT NULL and
	// CHECK-constrained; what frees it is deleted_at.
	var status string
	var email, passwordHash *string
	var deletedAt *time.Time
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT status, email, password_hash, deleted_at FROM users WHERE id = $1`, original.ID).
		Scan(&status, &email, &passwordHash, &deletedAt))

	assert.Equal(t, StatusDeleted, status)
	assert.Nil(t, email, "email must be scrubbed")
	assert.Nil(t, passwordHash, "the password hash must be scrubbed")
	assert.NotNil(t, deletedAt)

	// Both identifiers are reusable, which is the point of the partial indexes.
	reused, err := f.repo.CreateUser(ctx, NewUser{
		Handle:      f.handle("alice"),
		DisplayName: "Новый владелец логина",
		Email:       f.email("alice"),
	})
	require.NoError(t, err)
	assert.NotEqual(t, original.ID, reused.ID)

	// Deleting twice is not an error the caller has to special-case beyond
	// "already gone".
	require.ErrorIs(t, f.repo.SoftDeleteUser(ctx, original.ID), ErrUserNotFound)
}

func TestRepoUpdateProfile(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	user := f.mustUser(ctx, "alice", "correct-horse-battery")

	name := "Анна Петрова"
	updated, err := f.repo.UpdateProfile(ctx, user.ID, ProfileUpdate{DisplayName: &name})
	require.NoError(t, err)
	assert.Equal(t, name, updated.DisplayName)
	assert.Empty(t, updated.Bio, "an omitted field must be left alone")

	bio := "Готовлю борщ с 2015 года"
	updated, err = f.repo.UpdateProfile(ctx, user.ID, ProfileUpdate{Bio: &bio})
	require.NoError(t, err)
	assert.Equal(t, bio, updated.Bio)
	assert.Equal(t, name, updated.DisplayName, "an omitted field must survive a later update")

	empty := ""
	updated, err = f.repo.UpdateProfile(ctx, user.ID, ProfileUpdate{Bio: &empty})
	require.NoError(t, err)
	assert.Empty(t, updated.Bio, "an explicit empty value must clear the field")
}

func TestRepoUpdatePasswordHash(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	user := f.mustUser(ctx, "alice", "old-password-here")
	fresh, err := NewHasher(testParams).Hash("new-password-here")
	require.NoError(t, err)
	require.NoError(t, f.repo.UpdatePasswordHash(ctx, user.ID, fresh))

	reloaded, err := f.repo.FindByID(ctx, user.ID)
	require.NoError(t, err)
	require.NoError(t, NewHasher(testParams).Verify(reloaded.PasswordHash, "new-password-here"))
}

// ----------------------------------------------------------------- tokens --

func TestRepoRefreshTokenLifecycle(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	user := f.mustUser(ctx, "alice", "correct-horse-battery")
	raw, hash, err := newRefreshSecret()
	require.NoError(t, err)

	rec := RefreshRecord{
		ID:        uuid.New(),
		UserID:    user.ID,
		FamilyID:  uuid.New(),
		TokenHash: hash,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
		UserAgent: "SashasKitchen/1.0 (iPad)",
		IP:        "203.0.113.7",
	}
	require.NoError(t, f.repo.InsertRefreshToken(ctx, rec))

	found, err := f.repo.FindRefreshToken(ctx, HashRefreshToken(raw))
	require.NoError(t, err)
	assert.Equal(t, rec.ID, found.ID)
	assert.Equal(t, rec.FamilyID, found.FamilyID)
	assert.Equal(t, uuid.Nil, found.ParentID)
	assert.Equal(t, "SashasKitchen/1.0 (iPad)", found.UserAgent)
	assert.Equal(t, "203.0.113.7", found.IP)
	assert.False(t, found.Revoked())

	// The raw secret must not be recoverable from the row.
	var stored []byte
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT token_hash FROM refresh_tokens WHERE id = $1`, rec.ID).Scan(&stored))
	assert.Equal(t, HashRefreshToken(raw), stored)
	assert.NotContains(t, string(stored), raw)

	require.NoError(t, f.repo.RevokeRefreshToken(ctx, rec.ID))
	found, err = f.repo.FindRefreshToken(ctx, HashRefreshToken(raw))
	require.NoError(t, err, "a revoked row must stay findable so replay can be detected")
	assert.True(t, found.Revoked())

	_, err = f.repo.FindRefreshToken(ctx, HashRefreshToken("never-issued"))
	require.ErrorIs(t, err, ErrRefreshNotFound)
}

func TestRepoRefreshTokenUnparseableIPBecomesNull(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	user := f.mustUser(ctx, "alice", "correct-horse-battery")
	raw, hash, err := newRefreshSecret()
	require.NoError(t, err)

	// A bogus forwarding header must not be able to fail a login.
	require.NoError(t, f.repo.InsertRefreshToken(ctx, RefreshRecord{
		ID: uuid.New(), UserID: user.ID, FamilyID: uuid.New(), TokenHash: hash,
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		IP: "not-an-ip-address",
	}))

	found, err := f.repo.FindRefreshToken(ctx, HashRefreshToken(raw))
	require.NoError(t, err)
	assert.Empty(t, found.IP)
}

func TestRepoRotateIsAtomicAndSingleUse(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	user := f.mustUser(ctx, "alice", "correct-horse-battery")
	rot := NewRotator(f.repo, 24*time.Hour, time.Now)

	raw, first, err := rot.Issue(ctx, user.ID, SessionMeta{UserAgent: "iPad"})
	require.NoError(t, err)

	_, second, err := rot.Redeem(ctx, raw, SessionMeta{UserAgent: "iPad"})
	require.NoError(t, err)
	assert.Equal(t, first.FamilyID, second.FamilyID)
	assert.Equal(t, first.ID, second.ParentID)

	// Redeeming the same token a second time is reuse, and the chain dies.
	_, _, err = rot.Redeem(ctx, raw, SessionMeta{})
	require.ErrorIs(t, err, ErrRefreshReuse)

	var live int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE family_id = $1 AND revoked_at IS NULL`,
		first.FamilyID).Scan(&live))
	assert.Zero(t, live, "reuse must leave no live token in the family")
}

func TestRepoRevokeScopes(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	user := f.mustUser(ctx, "alice", "correct-horse-battery")
	rot := NewRotator(f.repo, 24*time.Hour, time.Now)

	_, phone, err := rot.Issue(ctx, user.ID, SessionMeta{UserAgent: "iPhone"})
	require.NoError(t, err)
	_, tablet, err := rot.Issue(ctx, user.ID, SessionMeta{UserAgent: "iPad"})
	require.NoError(t, err)

	n, err := f.repo.RevokeFamily(ctx, phone.FamilyID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	rec, err := f.repo.FindRefreshToken(ctx, tablet.TokenHash)
	require.NoError(t, err)
	assert.False(t, rec.Revoked(), "revoking one family must not touch another")

	n, err = f.repo.RevokeAllForUser(ctx, user.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n, "only the still-live token should be revoked")

	rec, err = f.repo.FindRefreshToken(ctx, tablet.TokenHash)
	require.NoError(t, err)
	assert.True(t, rec.Revoked())
}

// ------------------------------------------------------ external identity --

func TestRepoExternalIdentity(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	user := f.mustUser(ctx, "alice", "correct-horse-battery")
	subject := "001." + uuid.NewString()

	_, err := f.repo.FindUserByExternalIdentity(ctx, ProviderApple, subject)
	require.ErrorIs(t, err, ErrUserNotFound)

	require.NoError(t, f.repo.LinkExternalIdentity(ctx, user.ID, ProviderApple, subject, f.email("alice")))

	found, err := f.repo.FindUserByExternalIdentity(ctx, ProviderApple, subject)
	require.NoError(t, err)
	assert.Equal(t, user.ID, found.ID)

	// Linking the same subject to the same user again is idempotent.
	require.NoError(t, f.repo.LinkExternalIdentity(ctx, user.ID, ProviderApple, subject, f.email("alice")))

	// Another account must not be able to claim a subject that is already linked.
	other := f.mustUser(ctx, "bob", "another-password-x")
	err = f.repo.LinkExternalIdentity(ctx, other.ID, ProviderApple, subject, f.email("bob"))
	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, httpx.AsError(err).Status())

	// The original link is untouched.
	found, err = f.repo.FindUserByExternalIdentity(ctx, ProviderApple, subject)
	require.NoError(t, err)
	assert.Equal(t, user.ID, found.ID)

	// A soft-deleted owner takes the identity out of circulation.
	require.NoError(t, f.repo.SoftDeleteUser(ctx, user.ID))
	_, err = f.repo.FindUserByExternalIdentity(ctx, ProviderApple, subject)
	require.ErrorIs(t, err, ErrUserNotFound)
}

func TestRepoHandleTaken(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	taken, err := f.repo.HandleTaken(ctx, f.handle("alice"))
	require.NoError(t, err)
	assert.False(t, taken)

	user := f.mustUser(ctx, "alice", "correct-horse-battery")
	taken, err = f.repo.HandleTaken(ctx, f.handle("alice"))
	require.NoError(t, err)
	assert.True(t, taken)

	require.NoError(t, f.repo.SoftDeleteUser(ctx, user.ID))
	taken, err = f.repo.HandleTaken(ctx, f.handle("alice"))
	require.NoError(t, err)
	assert.False(t, taken, "a deleted account releases its handle")
}
