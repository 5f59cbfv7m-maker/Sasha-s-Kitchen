package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/config"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
)

// handlePattern is the same expression the users_handle_format CHECK enforces.
// Validating it here turns a database constraint violation into a field-level
// message the client can render next to the input.
var handlePattern = regexp.MustCompile(`^[a-z0-9_]{3,30}$`)

// emailPattern is deliberately loose. Strict RFC 5322 matching rejects valid
// addresses and proves nothing about deliverability; the real check is a
// confirmation mail.
var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s.]+(\.[^@\s.]+)+$`)

// Password bounds. The upper bound is a denial-of-service guard: argon2 cost
// grows with input length, and nobody needs a 10 KiB passphrase.
const (
	minPasswordLen = 8
	maxPasswordLen = 256
	maxDisplayName = 80
)

// Service is the use-case layer: it owns validation, the order of operations
// and the shape of the errors clients see.
type Service struct {
	repo    *Repo
	hasher  *Hasher
	tokens  *TokenService
	rotator *Rotator
	apple   *AppleVerifier
	now     func() time.Time
}

// ServiceOptions holds the collaborators a test may want to replace. Every
// field is optional; zero values fall back to production defaults.
type ServiceOptions struct {
	Hasher *Hasher
	Apple  *AppleVerifier
	Now    func() time.Time
}

// NewService assembles the auth service from configuration.
func NewService(repo *Repo, cfg config.AuthConfig, opts ServiceOptions) *Service {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	hasher := opts.Hasher
	if hasher == nil {
		hasher = NewHasher(DefaultParams)
	}
	tokens := NewTokenService(cfg.JWTSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL, now)
	apple := opts.Apple
	if apple == nil {
		apple = NewAppleVerifier(cfg.AppleClientID, cfg.AppleTeamID, AppleOptions{Now: now})
	}
	return &Service{
		repo:    repo,
		hasher:  hasher,
		tokens:  tokens,
		rotator: NewRotator(repo, tokens.RefreshTTL(), now),
		apple:   apple,
		now:     now,
	}
}

// Tokens exposes the token service so the middleware can share one instance.
func (s *Service) Tokens() *TokenService { return s.tokens }

// Repo exposes the repository for the middleware's per-request status check.
func (s *Service) Repo() *Repo { return s.repo }

// Session is the result of any successful authentication. The refresh secret is
// present exactly once, on its way to the client, and is never persisted or
// logged in this form.
type Session struct {
	User             User
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// RegisterInput is the register request after decoding.
type RegisterInput struct {
	Handle      string
	DisplayName string
	Email       string
	Password    string
}

// LoginInput is the login request. Login accepts either a handle or an email so
// the client does not have to ask the user which one they signed up with.
type LoginInput struct {
	Login    string
	Password string
}

// Register creates an account and signs the new user straight in.
func (s *Service) Register(ctx context.Context, in RegisterInput, meta SessionMeta) (Session, error) {
	in.Handle = strings.ToLower(strings.TrimSpace(in.Handle))
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.DisplayName = strings.TrimSpace(in.DisplayName)

	if err := validateRegister(in); err != nil {
		return Session{}, err
	}

	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return Session{}, httpx.Internal("Не удалось создать аккаунт").WithCause(err)
	}

	user, err := s.repo.CreateUser(ctx, NewUser{
		Handle:       in.Handle,
		DisplayName:  in.DisplayName,
		Email:        in.Email,
		PasswordHash: hash,
	})
	if err != nil {
		return Session{}, err
	}
	return s.startSession(ctx, user, meta)
}

// Login verifies credentials and opens a session.
//
// Two properties matter more than the happy path here. First, a wrong password
// and an unknown account return the same message and take the same time: the
// hash comparison runs against a throwaway hash when no user matched, so
// neither the response nor the latency reveals which handles exist. Second,
// the status check happens after the password check, so account state can only
// be probed by someone who already knows the password.
func (s *Service) Login(ctx context.Context, in LoginInput, meta SessionMeta) (Session, error) {
	login := strings.ToLower(strings.TrimSpace(in.Login))
	if login == "" || in.Password == "" {
		return Session{}, httpx.Validation(map[string]string{
			"login":    "Укажите логин или e-mail",
			"password": "Введите пароль",
		})
	}

	user, err := s.lookupForLogin(ctx, login)
	switch {
	case errors.Is(err, ErrUserNotFound):
		s.hasher.VerifyDummy(in.Password)
		return Session{}, errBadCredentials()
	case err != nil:
		return Session{}, err
	}

	if user.PasswordHash == "" {
		// An account created through Sign in with Apple has no password. Burn
		// the same work anyway so it is indistinguishable from a wrong one.
		s.hasher.VerifyDummy(in.Password)
		return Session{}, errBadCredentials()
	}
	if err := s.hasher.Verify(user.PasswordHash, in.Password); err != nil {
		if errors.Is(err, ErrPasswordMismatch) {
			return Session{}, errBadCredentials()
		}
		return Session{}, httpx.Internal("Не удалось проверить пароль").WithCause(err)
	}

	if err := checkUsable(user); err != nil {
		return Session{}, err
	}

	// The password was correct and is in hand, so an outdated hash can be
	// upgraded silently. A failure here must not fail the login.
	if s.hasher.NeedsRehash(user.PasswordHash) {
		if fresh, hashErr := s.hasher.Hash(in.Password); hashErr == nil {
			_ = s.repo.UpdatePasswordHash(ctx, user.ID, fresh)
		}
	}

	return s.startSession(ctx, user, meta)
}

// Refresh rotates a refresh token and issues a new access token.
func (s *Service) Refresh(ctx context.Context, refreshToken string, meta SessionMeta) (Session, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return Session{}, httpx.Validation(map[string]string{
			"refresh_token": "Укажите токен обновления",
		})
	}

	raw, rec, err := s.rotator.Redeem(ctx, refreshToken, meta)
	switch {
	case errors.Is(err, ErrRefreshReuse):
		// The chain was revoked by Redeem. Say nothing specific: the honest
		// client and the thief get the same answer.
		return Session{}, httpx.Unauthorized("Сессия недействительна, войдите заново")
	case errors.Is(err, ErrRefreshInvalid), errors.Is(err, ErrRefreshExpired):
		return Session{}, httpx.Unauthorized("Сессия недействительна, войдите заново")
	case err != nil:
		return Session{}, httpx.Internal("Не удалось обновить сессию").WithCause(err)
	}

	user, err := s.repo.FindByID(ctx, rec.UserID)
	if errors.Is(err, ErrUserNotFound) {
		// Deleted between issue and refresh: close the chain behind us.
		_, _ = s.repo.RevokeFamily(ctx, rec.FamilyID)
		return Session{}, httpx.Unauthorized("Сессия недействительна, войдите заново")
	}
	if err != nil {
		return Session{}, err
	}
	if err := checkUsable(user); err != nil {
		_, _ = s.repo.RevokeAllForUser(ctx, user.ID)
		return Session{}, err
	}

	access, accessExp, err := s.tokens.IssueAccess(user)
	if err != nil {
		return Session{}, httpx.Internal("Не удалось обновить сессию").WithCause(err)
	}
	return Session{
		User:             user,
		AccessToken:      access,
		AccessExpiresAt:  accessExp,
		RefreshToken:     raw,
		RefreshExpiresAt: rec.ExpiresAt,
	}, nil
}

// Logout ends one session. The family is revoked rather than the single token,
// because a family is exactly one device's chain — and the other devices keep
// working, which is what a user expects from signing out here.
//
// An unknown token is not an error: sign-out is idempotent, and reporting
// "no such token" would turn this endpoint into a token oracle.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return httpx.Validation(map[string]string{"refresh_token": "Укажите токен обновления"})
	}
	err := s.rotator.RevokeChain(ctx, refreshToken)
	if err != nil && !errors.Is(err, ErrRefreshInvalid) {
		return httpx.Internal("Не удалось завершить сессию").WithCause(err)
	}
	return nil
}

// DeleteAccount retires the caller's account and every session it holds.
func (s *Service) DeleteAccount(ctx context.Context, userID uuid.UUID) error {
	err := s.repo.SoftDeleteUser(ctx, userID)
	switch {
	case errors.Is(err, ErrUserNotFound):
		return httpx.NotFound("Аккаунт не найден")
	case err != nil:
		return httpx.Internal("Не удалось удалить аккаунт").WithCause(err)
	}
	return nil
}

// Me returns the current user.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (User, error) {
	user, err := s.repo.FindByID(ctx, userID)
	if errors.Is(err, ErrUserNotFound) {
		return User{}, httpx.NotFound("Аккаунт не найден")
	}
	return user, err
}

// lookupForLogin resolves a login that may be either a handle or an email.
func (s *Service) lookupForLogin(ctx context.Context, login string) (User, error) {
	if strings.Contains(login, "@") {
		return s.repo.FindByEmail(ctx, login)
	}
	return s.repo.FindByHandle(ctx, login)
}

// startSession issues both tokens for an authenticated user.
func (s *Service) startSession(ctx context.Context, user User, meta SessionMeta) (Session, error) {
	access, accessExp, err := s.tokens.IssueAccess(user)
	if err != nil {
		return Session{}, httpx.Internal("Не удалось создать сессию").WithCause(err)
	}
	raw, rec, err := s.rotator.Issue(ctx, user.ID, meta)
	if err != nil {
		return Session{}, httpx.Internal("Не удалось создать сессию").WithCause(err)
	}
	return Session{
		User:             user,
		AccessToken:      access,
		AccessExpiresAt:  accessExp,
		RefreshToken:     raw,
		RefreshExpiresAt: rec.ExpiresAt,
	}, nil
}

// checkUsable rejects accounts that exist but may not sign in.
func checkUsable(u User) error {
	switch u.Status {
	case StatusActive:
		return nil
	case StatusSuspended:
		return httpx.Forbidden("Аккаунт заблокирован. Напишите в поддержку")
	default:
		return httpx.Unauthorized("Аккаунт недоступен")
	}
}

// errBadCredentials is the single answer to every failed login. One message for
// both "no such user" and "wrong password" is what keeps the endpoint from
// enumerating accounts.
func errBadCredentials() error {
	return httpx.Unauthorized("Неверный логин или пароль")
}

// ------------------------------------------------------------ validation --

func validateRegister(in RegisterInput) error {
	fields := map[string]string{}

	if msg := validateHandle(in.Handle); msg != "" {
		fields["handle"] = msg
	}
	if in.DisplayName == "" {
		fields["display_name"] = "Укажите имя, которое увидят другие"
	} else if utf8.RuneCountInString(in.DisplayName) > maxDisplayName {
		fields["display_name"] = fmt.Sprintf("Имя не длиннее %d символов", maxDisplayName)
	}
	if in.Email == "" {
		fields["email"] = "Укажите e-mail"
	} else if !emailPattern.MatchString(in.Email) {
		fields["email"] = "Проверьте адрес: он не похож на e-mail"
	}
	if msg := validatePassword(in.Password, in.Handle, in.Email); msg != "" {
		fields["password"] = msg
	}

	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// validateHandle returns a Russian message, or "" when the handle is fine.
func validateHandle(handle string) string {
	switch {
	case handle == "":
		return "Придумайте логин"
	case len(handle) < 3:
		return "Логин не короче 3 символов"
	case len(handle) > 30:
		return "Логин не длиннее 30 символов"
	case !handlePattern.MatchString(handle):
		return "Логин может содержать только строчные латинские буквы, цифры и подчёркивание"
	default:
		return ""
	}
}

// validatePassword enforces a floor that blocks the passwords that actually get
// broken, without the character-class theatre that pushes people to "Passw0rd!".
func validatePassword(password, handle, email string) string {
	switch {
	case password == "":
		return "Придумайте пароль"
	case utf8.RuneCountInString(password) < minPasswordLen:
		return fmt.Sprintf("Пароль не короче %d символов", minPasswordLen)
	case utf8.RuneCountInString(password) > maxPasswordLen:
		return fmt.Sprintf("Пароль не длиннее %d символов", maxPasswordLen)
	case isAllDigits(password):
		return "Пароль не должен состоять только из цифр"
	case strings.EqualFold(password, handle):
		return "Пароль не должен совпадать с логином"
	case email != "" && strings.EqualFold(password, email):
		return "Пароль не должен совпадать с e-mail"
	default:
		return ""
	}
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}
