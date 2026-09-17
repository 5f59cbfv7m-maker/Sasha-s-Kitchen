package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Role names carried in the access token so a handler can authorise without a
// second lookup. They mirror the boolean columns on users.
const (
	RoleUser   = "user"
	RoleAuthor = "author"
	RoleAdmin  = "admin"
)

// tokenIssuer is the iss claim. It is validated on parse, so a token minted for
// some other service with the same secret cannot be replayed here.
const tokenIssuer = "sashas-kitchen"

// refreshTokenBytes is the entropy behind an opaque refresh token. 32 bytes is
// the floor: these are bearer credentials with a month-long life.
const refreshTokenBytes = 32

// Refresh-token failures. Handlers map all of them to one indistinguishable
// 401 so a caller cannot probe which tokens ever existed.
var (
	// ErrRefreshNotFound is returned by a RefreshStore when no row matches.
	ErrRefreshNotFound = errors.New("auth: refresh token not found")
	// ErrRefreshInvalid means the presented token is not a live token.
	ErrRefreshInvalid = errors.New("auth: refresh token invalid")
	// ErrRefreshExpired means the token was genuine but is past its lifetime.
	ErrRefreshExpired = errors.New("auth: refresh token expired")
	// ErrRefreshReuse means an already-revoked token was presented again. That
	// is the signature of a stolen chain, and the family has been revoked.
	ErrRefreshReuse = errors.New("auth: refresh token reuse detected")
)

// AccessClaims is the JWT payload. RegisteredClaims supplies sub, exp, iat and
// jti; roles is the only custom claim, kept small because the token travels on
// every request.
type AccessClaims struct {
	Roles []string `json:"roles"`
	jwt.RegisteredClaims
}

// TokenService mints and validates access tokens and mints refresh secrets.
type TokenService struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

// NewTokenService builds the service from configuration. now may be nil, in
// which case time.Now is used; tests inject a clock to exercise expiry without
// sleeping.
func NewTokenService(secret []byte, accessTTL, refreshTTL time.Duration, now func() time.Time) *TokenService {
	if now == nil {
		now = time.Now
	}
	if accessTTL <= 0 {
		accessTTL = 15 * time.Minute
	}
	if refreshTTL <= 0 {
		refreshTTL = 30 * 24 * time.Hour
	}
	return &TokenService{secret: secret, accessTTL: accessTTL, refreshTTL: refreshTTL, now: now}
}

// AccessTTL exposes the configured access-token lifetime for the expires_in
// field of a token response.
func (t *TokenService) AccessTTL() time.Duration { return t.accessTTL }

// RefreshTTL exposes the configured refresh-token lifetime.
func (t *TokenService) RefreshTTL() time.Duration { return t.refreshTTL }

// RolesFor turns the user's flags into the roles claim. Every account carries
// RoleUser so a handler can require "any signed-in caller" uniformly.
func RolesFor(u User) []string {
	roles := []string{RoleUser}
	if u.IsAuthor {
		roles = append(roles, RoleAuthor)
	}
	if u.IsAdmin {
		roles = append(roles, RoleAdmin)
	}
	return roles
}

// IssueAccess mints a short-lived HS256 access token for u and returns it with
// its expiry.
func (t *TokenService) IssueAccess(u User) (string, time.Time, error) {
	now := t.now()
	expires := now.Add(t.accessTTL)

	claims := AccessClaims{
		Roles: RolesFor(u),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID.String(),
			Issuer:    tokenIssuer,
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expires),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: sign access token: %w", err)
	}
	return signed, expires, nil
}

// ParseAccess validates a token and returns its claims.
//
// The accepted algorithm is pinned to HS256. Without that pin a caller could
// present an unsigned ("alg":"none") token, or an RS256 token whose "public
// key" is our HMAC secret — both are classic JWT confusion attacks.
func (t *TokenService) ParseAccess(token string) (*AccessClaims, error) {
	claims := &AccessClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims,
		func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil {
		return nil, fmt.Errorf("auth: parse access token: %w", err)
	}
	if !parsed.Valid {
		return nil, errors.New("auth: access token invalid")
	}
	return claims, nil
}

// SubjectID reads the sub claim as a user id.
func (c *AccessClaims) SubjectID() (uuid.UUID, error) {
	return uuid.Parse(c.Subject)
}

// HashRefreshToken is the one-way mapping from the secret a client holds to the
// value stored in refresh_tokens.token_hash. SHA-256 is right here and bcrypt
// would be wrong: the input is already 32 bytes of uniform randomness, so there
// is nothing to slow-hash against, and lookups must be a single indexed probe.
func HashRefreshToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// newRefreshSecret returns a fresh opaque token and its stored hash. The raw
// value exists only in the response to the client; it is never logged, and the
// only copy we keep is the hash.
func newRefreshSecret() (raw string, hash []byte, err error) {
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("auth: read refresh token entropy: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashRefreshToken(raw), nil
}

// RefreshRecord is one row of refresh_tokens.
type RefreshRecord struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	FamilyID  uuid.UUID
	ParentID  uuid.UUID // uuid.Nil for the first token of a family
	TokenHash []byte
	IssuedAt  time.Time
	ExpiresAt time.Time
	RevokedAt time.Time // zero value means still live
	UserAgent string
	IP        string
}

// Revoked reports whether the record has already been retired.
func (r RefreshRecord) Revoked() bool { return !r.RevokedAt.IsZero() }

// SessionMeta is the best-effort provenance recorded with a refresh token so a
// user can later be shown where their sessions came from.
type SessionMeta struct {
	UserAgent string
	IP        string
}

// RefreshStore is the persistence the rotation logic needs. It is an interface
// so rotation and reuse detection can be unit tested against an in-memory fake,
// with no database in the loop.
type RefreshStore interface {
	// InsertRefreshToken stores a newly minted token.
	InsertRefreshToken(ctx context.Context, rec RefreshRecord) error
	// FindRefreshToken looks a token up by hash, returning revoked rows too —
	// reuse detection depends on revoked rows staying findable.
	FindRefreshToken(ctx context.Context, hash []byte) (RefreshRecord, error)
	// RotateRefreshToken revokes oldID and inserts next atomically, so a crash
	// between the two cannot leave a user with no valid token or with two.
	RotateRefreshToken(ctx context.Context, oldID uuid.UUID, next RefreshRecord) error
	// RevokeFamily retires every live token descended from one login.
	RevokeFamily(ctx context.Context, familyID uuid.UUID) (int64, error)
	// RevokeAllForUser retires every live token the user holds.
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) (int64, error)
}

// Rotator implements refresh-token rotation with reuse detection.
type Rotator struct {
	store RefreshStore
	ttl   time.Duration
	now   func() time.Time
}

// NewRotator wires a rotator over a store.
func NewRotator(store RefreshStore, ttl time.Duration, now func() time.Time) *Rotator {
	if now == nil {
		now = time.Now
	}
	return &Rotator{store: store, ttl: ttl, now: now}
}

// Issue starts a new token family. One family is one login on one device, which
// is the unit a reuse response is allowed to destroy.
func (r *Rotator) Issue(ctx context.Context, userID uuid.UUID, meta SessionMeta) (string, RefreshRecord, error) {
	raw, hash, err := newRefreshSecret()
	if err != nil {
		return "", RefreshRecord{}, err
	}
	now := r.now()
	rec := RefreshRecord{
		ID:        uuid.New(),
		UserID:    userID,
		FamilyID:  uuid.New(),
		TokenHash: hash,
		IssuedAt:  now,
		ExpiresAt: now.Add(r.ttl),
		UserAgent: meta.UserAgent,
		IP:        meta.IP,
	}
	if err := r.store.InsertRefreshToken(ctx, rec); err != nil {
		return "", RefreshRecord{}, err
	}
	return raw, rec, nil
}

// Redeem exchanges a refresh token for a new one, retiring the presented token.
//
// Presenting a token that was already retired means two parties hold the same
// chain: the legitimate client rotated, and someone else replayed the old
// value. There is no way to tell which caller is the thief, so the whole family
// is revoked and both are forced to sign in again. Only that family — the
// user's other devices are untouched, because signing someone out everywhere
// over one leaked chain is a real cost with no extra safety.
func (r *Rotator) Redeem(ctx context.Context, raw string, meta SessionMeta) (string, RefreshRecord, error) {
	current, err := r.store.FindRefreshToken(ctx, HashRefreshToken(raw))
	switch {
	case errors.Is(err, ErrRefreshNotFound):
		return "", RefreshRecord{}, ErrRefreshInvalid
	case err != nil:
		return "", RefreshRecord{}, err
	}

	if current.Revoked() {
		if _, revokeErr := r.store.RevokeFamily(ctx, current.FamilyID); revokeErr != nil {
			return "", RefreshRecord{}, revokeErr
		}
		return "", current, ErrRefreshReuse
	}

	now := r.now()
	if !now.Before(current.ExpiresAt) {
		// Expiry is not evidence of theft, but the chain cannot continue past
		// it either, so the family is closed out rather than left dangling.
		if _, revokeErr := r.store.RevokeFamily(ctx, current.FamilyID); revokeErr != nil {
			return "", RefreshRecord{}, revokeErr
		}
		return "", RefreshRecord{}, ErrRefreshExpired
	}

	nextRaw, hash, err := newRefreshSecret()
	if err != nil {
		return "", RefreshRecord{}, err
	}
	next := RefreshRecord{
		ID:        uuid.New(),
		UserID:    current.UserID,
		FamilyID:  current.FamilyID,
		ParentID:  current.ID,
		TokenHash: hash,
		IssuedAt:  now,
		ExpiresAt: now.Add(r.ttl),
		UserAgent: meta.UserAgent,
		IP:        meta.IP,
	}
	if err := r.store.RotateRefreshToken(ctx, current.ID, next); err != nil {
		return "", RefreshRecord{}, err
	}
	return nextRaw, next, nil
}

// RevokeChain retires the family a token belongs to. This is what sign-out
// does: it ends the session on that device without touching the others.
func (r *Rotator) RevokeChain(ctx context.Context, raw string) error {
	rec, err := r.store.FindRefreshToken(ctx, HashRefreshToken(raw))
	switch {
	case errors.Is(err, ErrRefreshNotFound):
		return ErrRefreshInvalid
	case err != nil:
		return err
	}
	_, err = r.store.RevokeFamily(ctx, rec.FamilyID)
	return err
}
