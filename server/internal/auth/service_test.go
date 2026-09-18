package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
)

// validationFields runs err through the API error mapping and returns the
// per-field messages, failing the test when err is not a 422.
func validationFields(t *testing.T, err error) map[string]string {
	t.Helper()
	require.Error(t, err)
	apiErr := httpx.AsError(err)
	require.Equal(t, httpx.CodeValidation, apiErr.Code, "expected a validation error, got %v", apiErr)
	return apiErr.Fields
}

func TestServiceRegisterValidation(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	valid := RegisterInput{
		Handle:      f.handle("alice"),
		DisplayName: "Анна",
		Email:       f.email("alice"),
		Password:    "correct-horse-battery",
	}

	tests := []struct {
		name      string
		mutate    func(*RegisterInput)
		wantField string
		wantMsg   string
	}{
		{"empty handle", func(in *RegisterInput) { in.Handle = "" }, "handle", "Придумайте логин"},
		{"short handle", func(in *RegisterInput) { in.Handle = "ab" }, "handle", "Логин не короче 3 символов"},
		{"long handle", func(in *RegisterInput) { in.Handle = strings.Repeat("a", 31) }, "handle", "Логин не длиннее 30 символов"},
		{"uppercase handle is folded, not rejected", func(in *RegisterInput) { in.Handle = strings.ToUpper(in.Handle) }, "", ""},
		{"cyrillic handle", func(in *RegisterInput) { in.Handle = "саша_повар" }, "handle",
			"Логин может содержать только строчные латинские буквы, цифры и подчёркивание"},
		{"handle with dash", func(in *RegisterInput) { in.Handle = "anna-maria" }, "handle",
			"Логин может содержать только строчные латинские буквы, цифры и подчёркивание"},
		{"empty display name", func(in *RegisterInput) { in.DisplayName = "  " }, "display_name", "Укажите имя, которое увидят другие"},
		{"long display name", func(in *RegisterInput) { in.DisplayName = strings.Repeat("я", 81) }, "display_name", "Имя не длиннее 80 символов"},
		{"empty email", func(in *RegisterInput) { in.Email = "" }, "email", "Укажите e-mail"},
		{"malformed email", func(in *RegisterInput) { in.Email = "not-an-email" }, "email", "Проверьте адрес: он не похож на e-mail"},
		{"empty password", func(in *RegisterInput) { in.Password = "" }, "password", "Придумайте пароль"},
		{"short password", func(in *RegisterInput) { in.Password = "1234567" }, "password", "Пароль не короче 8 символов"},
		{"all-digit password", func(in *RegisterInput) { in.Password = "123456789012" }, "password", "Пароль не должен состоять только из цифр"},
		{"password equals handle", func(in *RegisterInput) { in.Password = in.Handle }, "password", "Пароль не должен совпадать с логином"},
		{"overlong password", func(in *RegisterInput) { in.Password = strings.Repeat("a", 257) }, "password", "Пароль не длиннее 256 символов"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := valid
			tc.mutate(&in)

			session, err := f.svc.Register(ctx, in, SessionMeta{})
			if tc.wantField == "" {
				require.NoError(t, err)
				f.track(session.User.ID)
				return
			}
			fields := validationFields(t, err)
			assert.Equal(t, tc.wantMsg, fields[tc.wantField],
				"field %q should carry a Russian message, got %v", tc.wantField, fields)
		})
	}
}

func TestServiceRegisterAndLogin(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	const password = "correct-horse-battery"
	session, err := f.svc.Register(ctx, RegisterInput{
		Handle:      "  " + strings.ToUpper(f.handle("alice")) + "  ",
		DisplayName: "  Анна  ",
		Email:       "  " + strings.ToUpper(f.email("alice")) + " ",
		Password:    password,
	}, SessionMeta{UserAgent: "iPad", IP: "203.0.113.5"})
	require.NoError(t, err)

	assert.Equal(t, f.handle("alice"), session.User.Handle, "the handle must be folded to lower case")
	assert.Equal(t, "Анна", session.User.DisplayName, "surrounding whitespace must be trimmed")
	assert.NotEmpty(t, session.AccessToken)
	assert.NotEmpty(t, session.RefreshToken)

	// The stored hash must be argon2id and must not be the password.
	stored, err := f.repo.FindByID(ctx, session.User.ID)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(stored.PasswordHash, "$argon2id$"))
	assert.NotContains(t, stored.PasswordHash, password)

	tests := []struct {
		name  string
		login string
	}{
		{"by handle", f.handle("alice")},
		{"by handle uppercase", strings.ToUpper(f.handle("alice"))},
		{"by email", f.email("alice")},
		{"by email with spaces", "  " + f.email("alice") + "  "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.svc.Login(ctx, LoginInput{Login: tc.login, Password: password}, SessionMeta{})
			require.NoError(t, err)
			assert.Equal(t, session.User.ID, got.User.ID)
		})
	}
}

func TestServiceLoginFailuresAreIndistinguishable(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	_, err := f.svc.Register(ctx, RegisterInput{
		Handle:      f.handle("alice"),
		DisplayName: "Анна",
		Email:       f.email("alice"),
		Password:    "correct-horse-battery",
	}, SessionMeta{})
	require.NoError(t, err)

	tests := []struct {
		name  string
		input LoginInput
	}{
		{"wrong password", LoginInput{Login: f.handle("alice"), Password: "wrong-password-here"}},
		{"unknown handle", LoginInput{Login: f.handle("nobody"), Password: "correct-horse-battery"}},
		{"unknown email", LoginInput{Login: f.email("nobody"), Password: "correct-horse-battery"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			started := time.Now()
			_, err := f.svc.Login(ctx, tc.input, SessionMeta{})
			elapsed := time.Since(started)

			apiErr := httpx.AsError(err)
			assert.Equal(t, http.StatusUnauthorized, apiErr.Status())
			assert.Equal(t, "Неверный логин или пароль", apiErr.Message,
				"every failure mode must answer with the same message")

			// An absent account must still pay for a hash comparison. Without
			// that, response latency alone enumerates who has an account here.
			assert.Greater(t, elapsed, time.Millisecond,
				"a failed login must run a real hash comparison, not return early")
		})
	}
}

func TestServiceLoginRejectsMissingFields(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	_, err := f.svc.Login(ctx, LoginInput{}, SessionMeta{})
	fields := validationFields(t, err)
	assert.Equal(t, "Укажите логин или e-mail", fields["login"])
	assert.Equal(t, "Введите пароль", fields["password"])
}

func TestServiceLoginRejectsSuspendedAndDeleted(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	const password = "correct-horse-battery"
	session, err := f.svc.Register(ctx, RegisterInput{
		Handle:      f.handle("alice"),
		DisplayName: "Анна",
		Email:       f.email("alice"),
		Password:    password,
	}, SessionMeta{})
	require.NoError(t, err)

	require.NoError(t, f.repo.SetUserStatus(ctx, session.User.ID, StatusSuspended))
	_, err = f.svc.Login(ctx, LoginInput{Login: f.handle("alice"), Password: password}, SessionMeta{})
	apiErr := httpx.AsError(err)
	assert.Equal(t, http.StatusForbidden, apiErr.Status())
	assert.Equal(t, "Аккаунт заблокирован. Напишите в поддержку", apiErr.Message)

	// A wrong password on a suspended account still reads as bad credentials:
	// account state must not be probeable without knowing the password.
	_, err = f.svc.Login(ctx, LoginInput{Login: f.handle("alice"), Password: "nope-nope-nope"}, SessionMeta{})
	assert.Equal(t, http.StatusUnauthorized, httpx.AsError(err).Status())

	require.NoError(t, f.repo.SetUserStatus(ctx, session.User.ID, StatusActive))
	require.NoError(t, f.repo.SoftDeleteUser(ctx, session.User.ID))
	_, err = f.svc.Login(ctx, LoginInput{Login: f.handle("alice"), Password: password}, SessionMeta{})
	assert.Equal(t, http.StatusUnauthorized, httpx.AsError(err).Status(),
		"a deleted account must be indistinguishable from one that never existed")
}

func TestServiceLoginRejectsPasswordlessAccount(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	// An Apple-only account has no password hash at all.
	user := f.mustUser(ctx, "apple_only", "")
	require.Empty(t, user.PasswordHash)

	_, err := f.svc.Login(ctx, LoginInput{Login: f.handle("apple_only"), Password: "anything-at-all"}, SessionMeta{})
	assert.Equal(t, http.StatusUnauthorized, httpx.AsError(err).Status())

	// An empty password must not match an empty hash.
	_, err = f.svc.Login(ctx, LoginInput{Login: f.handle("apple_only"), Password: ""}, SessionMeta{})
	require.Error(t, err)
}

func TestServiceLoginRehashesOutdatedHash(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	const password = "correct-horse-battery"

	// Simulate an account hashed under weaker settings than we now use.
	weak := NewHasher(Params{Memory: 8 * 1024, Time: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	weakHash, err := weak.Hash(password)
	require.NoError(t, err)

	user, err := f.repo.CreateUser(ctx, NewUser{
		Handle:       f.handle("legacy"),
		DisplayName:  "Старый аккаунт",
		Email:        f.email("legacy"),
		PasswordHash: weakHash,
	})
	require.NoError(t, err)

	// The service now hashes with stronger parameters.
	strong := NewHasher(Params{Memory: 16 * 1024, Time: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	f.svc.hasher = strong
	require.True(t, strong.NeedsRehash(weakHash))

	_, err = f.svc.Login(ctx, LoginInput{Login: f.handle("legacy"), Password: password}, SessionMeta{})
	require.NoError(t, err, "an outdated hash must still let the user in")

	reloaded, err := f.repo.FindByID(ctx, user.ID)
	require.NoError(t, err)
	assert.NotEqual(t, weakHash, reloaded.PasswordHash, "the hash should have been upgraded in place")
	assert.False(t, strong.NeedsRehash(reloaded.PasswordHash))
	require.NoError(t, strong.Verify(reloaded.PasswordHash, password),
		"the upgraded hash must still verify the same password")
}

func TestServiceRefreshRotatesAndDetectsReuse(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	const password = "correct-horse-battery"
	phone, err := f.svc.Register(ctx, RegisterInput{
		Handle:      f.handle("alice"),
		DisplayName: "Анна",
		Email:       f.email("alice"),
		Password:    password,
	}, SessionMeta{UserAgent: "iPhone"})
	require.NoError(t, err)

	tablet, err := f.svc.Login(ctx, LoginInput{Login: f.handle("alice"), Password: password},
		SessionMeta{UserAgent: "iPad"})
	require.NoError(t, err)

	rotated, err := f.svc.Refresh(ctx, phone.RefreshToken, SessionMeta{UserAgent: "iPhone"})
	require.NoError(t, err)
	assert.NotEqual(t, phone.RefreshToken, rotated.RefreshToken)
	assert.NotEmpty(t, rotated.AccessToken)
	assert.Equal(t, phone.User.ID, rotated.User.ID)

	// Replaying the retired token is treated as theft.
	_, err = f.svc.Refresh(ctx, phone.RefreshToken, SessionMeta{UserAgent: "thief"})
	apiErr := httpx.AsError(err)
	assert.Equal(t, http.StatusUnauthorized, apiErr.Status())
	assert.Equal(t, "Сессия недействительна, войдите заново", apiErr.Message)

	// The compromised chain is gone, including the token the honest phone holds.
	_, err = f.svc.Refresh(ctx, rotated.RefreshToken, SessionMeta{UserAgent: "iPhone"})
	assert.Equal(t, http.StatusUnauthorized, httpx.AsError(err).Status())

	// The tablet was never part of that chain and keeps working.
	_, err = f.svc.Refresh(ctx, tablet.RefreshToken, SessionMeta{UserAgent: "iPad"})
	require.NoError(t, err, "one leaked chain must not sign the user out of their other devices")
}

func TestServiceRefreshFailures(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	session, err := f.svc.Register(ctx, RegisterInput{
		Handle:      f.handle("alice"),
		DisplayName: "Анна",
		Email:       f.email("alice"),
		Password:    "correct-horse-battery",
	}, SessionMeta{})
	require.NoError(t, err)

	t.Run("empty token", func(t *testing.T) {
		_, err := f.svc.Refresh(ctx, "  ", SessionMeta{})
		fields := validationFields(t, err)
		assert.Equal(t, "Укажите токен обновления", fields["refresh_token"])
	})

	t.Run("unknown token", func(t *testing.T) {
		_, err := f.svc.Refresh(ctx, "never-issued-token", SessionMeta{})
		assert.Equal(t, http.StatusUnauthorized, httpx.AsError(err).Status())
	})

	t.Run("expired token", func(t *testing.T) {
		f.clock.Advance(25 * time.Hour) // the fixture's refresh TTL is 24h
		defer f.clock.Advance(-25 * time.Hour)

		_, err := f.svc.Refresh(ctx, session.RefreshToken, SessionMeta{})
		apiErr := httpx.AsError(err)
		assert.Equal(t, http.StatusUnauthorized, apiErr.Status())
		assert.Equal(t, "Сессия недействительна, войдите заново", apiErr.Message)
	})
}

func TestServiceRefreshStopsForSuspendedAndDeleted(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	tests := []struct {
		name       string
		disable    func(t *testing.T, ctx context.Context, f *fixture, userID interface{ String() string })
		wantStatus int
	}{}
	_ = tests // the two cases below are clearer written out than tabulated

	t.Run("suspended", func(t *testing.T) {
		session, err := f.svc.Register(ctx, RegisterInput{
			Handle:      f.handle("susp"),
			DisplayName: "Анна",
			Email:       f.email("susp"),
			Password:    "correct-horse-battery",
		}, SessionMeta{})
		require.NoError(t, err)

		require.NoError(t, f.repo.SetUserStatus(ctx, session.User.ID, StatusSuspended))
		_, err = f.svc.Refresh(ctx, session.RefreshToken, SessionMeta{})
		assert.Equal(t, http.StatusForbidden, httpx.AsError(err).Status())

		// Suspension must also have burned the tokens, not merely blocked them.
		require.NoError(t, f.repo.SetUserStatus(ctx, session.User.ID, StatusActive))
		_, err = f.svc.Refresh(ctx, session.RefreshToken, SessionMeta{})
		assert.Equal(t, http.StatusUnauthorized, httpx.AsError(err).Status(),
			"tokens revoked during suspension must not come back to life")
	})

	t.Run("deleted", func(t *testing.T) {
		session, err := f.svc.Register(ctx, RegisterInput{
			Handle:      f.handle("del"),
			DisplayName: "Анна",
			Email:       f.email("del"),
			Password:    "correct-horse-battery",
		}, SessionMeta{})
		require.NoError(t, err)

		require.NoError(t, f.svc.DeleteAccount(ctx, session.User.ID))
		_, err = f.svc.Refresh(ctx, session.RefreshToken, SessionMeta{})
		assert.Equal(t, http.StatusUnauthorized, httpx.AsError(err).Status())
	})
}

func TestServiceLogout(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	const password = "correct-horse-battery"
	phone, err := f.svc.Register(ctx, RegisterInput{
		Handle:      f.handle("alice"),
		DisplayName: "Анна",
		Email:       f.email("alice"),
		Password:    password,
	}, SessionMeta{UserAgent: "iPhone"})
	require.NoError(t, err)

	tablet, err := f.svc.Login(ctx, LoginInput{Login: f.handle("alice"), Password: password},
		SessionMeta{UserAgent: "iPad"})
	require.NoError(t, err)

	require.NoError(t, f.svc.Logout(ctx, phone.RefreshToken))

	_, err = f.svc.Refresh(ctx, phone.RefreshToken, SessionMeta{})
	assert.Equal(t, http.StatusUnauthorized, httpx.AsError(err).Status())

	_, err = f.svc.Refresh(ctx, tablet.RefreshToken, SessionMeta{})
	require.NoError(t, err, "signing out one device must leave the others signed in")

	// Sign-out is idempotent and never confirms whether a token existed.
	require.NoError(t, f.svc.Logout(ctx, phone.RefreshToken))
	require.NoError(t, f.svc.Logout(ctx, "a-token-that-never-existed"))

	err = f.svc.Logout(ctx, "   ")
	assert.Equal(t, "Укажите токен обновления", validationFields(t, err)["refresh_token"])
}

func TestServiceDeleteAccount(t *testing.T) {
	f := newFixture(t, nil)
	ctx := t.Context()

	const password = "correct-horse-battery"
	session, err := f.svc.Register(ctx, RegisterInput{
		Handle:      f.handle("alice"),
		DisplayName: "Анна",
		Email:       f.email("alice"),
		Password:    password,
	}, SessionMeta{})
	require.NoError(t, err)

	require.NoError(t, f.svc.DeleteAccount(ctx, session.User.ID))

	_, err = f.svc.Me(ctx, session.User.ID)
	assert.Equal(t, http.StatusNotFound, httpx.AsError(err).Status())

	// The handle is immediately reusable by someone else.
	replacement, err := f.svc.Register(ctx, RegisterInput{
		Handle:      f.handle("alice"),
		DisplayName: "Другая Анна",
		Email:       f.email("alice2"),
		Password:    password,
	}, SessionMeta{})
	require.NoError(t, err)
	assert.NotEqual(t, session.User.ID, replacement.User.ID)

	// Deleting an account that is already gone is a 404, not a 500.
	err = f.svc.DeleteAccount(ctx, session.User.ID)
	assert.Equal(t, http.StatusNotFound, httpx.AsError(err).Status())
}
