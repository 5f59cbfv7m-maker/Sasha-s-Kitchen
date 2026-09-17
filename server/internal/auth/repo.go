package auth

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
)

// Account statuses, mirroring the CHECK constraint on users.status.
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
	StatusDeleted   = "deleted"
)

// Identity providers, mirroring the CHECK constraint on external_identities.provider.
const ProviderApple = "apple"

// ErrUserNotFound is returned when no live user matches the lookup. Soft-deleted
// rows never match: every query filters on deleted_at IS NULL.
var ErrUserNotFound = errors.New("auth: user not found")

// Postgres unique-violation code.
const pgUniqueViolation = "23505"

// User is a row of the users table. PasswordHash is deliberately part of this
// type and deliberately absent from every response DTO — the separation is what
// keeps it from being serialised by accident.
type User struct {
	ID            uuid.UUID
	Handle        string
	DisplayName   string
	Email         string
	PasswordHash  string
	Bio           string
	AvatarMediaID uuid.UUID
	IsAuthor      bool
	IsAdmin       bool
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewUser is the input to CreateUser.
type NewUser struct {
	Handle       string
	DisplayName  string
	Email        string
	PasswordHash string
	IsAuthor     bool
}

// ProfileUpdate carries the fields a user may change about themselves. A nil
// pointer means "leave as is", which is what distinguishes an omitted field
// from an explicit clear.
type ProfileUpdate struct {
	DisplayName *string
	Bio         *string
	AvatarID    *uuid.UUID
}

// Repo is the pgx-backed persistence for everything in this package.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo wires a repository over an existing pool.
func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// userColumns is the single projection every user read uses, so a new column
// cannot be added to one query and forgotten in another.
const userColumns = `id, handle, display_name, email, password_hash, bio,
	avatar_media_id, is_author, is_admin, status, created_at, updated_at`

// userColumnsJoined is the same projection qualified with an alias, for the one
// read that reaches users through a join.
var userColumnsJoined = prefixed(userColumns, "u")

// scanUser reads one row in userColumns order, mapping SQL NULLs to zero values.
func scanUser(row pgx.Row) (User, error) {
	var (
		u        User
		email    *string
		pwd      *string
		bio      *string
		avatarID *uuid.UUID
	)
	err := row.Scan(&u.ID, &u.Handle, &u.DisplayName, &email, &pwd, &bio,
		&avatarID, &u.IsAuthor, &u.IsAdmin, &u.Status, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: scan user: %w", err)
	}
	if email != nil {
		u.Email = *email
	}
	if pwd != nil {
		u.PasswordHash = *pwd
	}
	if bio != nil {
		u.Bio = *bio
	}
	if avatarID != nil {
		u.AvatarMediaID = *avatarID
	}
	return u, nil
}

// CreateUser inserts a new account. A collision on the partial unique indexes
// surfaces as a 409 naming the field, because "handle taken" and "email taken"
// need different fixes from the user.
func (r *Repo) CreateUser(ctx context.Context, in NewUser) (User, error) {
	const q = `
		INSERT INTO users (handle, display_name, email, password_hash, is_author)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + userColumns

	u, err := scanUser(r.pool.QueryRow(ctx, q,
		in.Handle, in.DisplayName, nullString(in.Email), nullString(in.PasswordHash), in.IsAuthor))
	if err != nil {
		return User{}, mapUserWriteError(err)
	}
	return u, nil
}

// FindByID returns a live user by primary key.
func (r *Repo) FindByID(ctx context.Context, id uuid.UUID) (User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = $1 AND deleted_at IS NULL`
	return scanUser(r.pool.QueryRow(ctx, q, id))
}

// FindByHandle returns a live user by handle. The column is citext, so the
// match is case-insensitive without a function on the indexed side.
func (r *Repo) FindByHandle(ctx context.Context, handle string) (User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE handle = $1 AND deleted_at IS NULL`
	return scanUser(r.pool.QueryRow(ctx, q, handle))
}

// FindByEmail returns a live user by email.
func (r *Repo) FindByEmail(ctx context.Context, email string) (User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE email = $1 AND deleted_at IS NULL`
	return scanUser(r.pool.QueryRow(ctx, q, email))
}

// HandleTaken reports whether a live account already holds the handle.
func (r *Repo) HandleTaken(ctx context.Context, handle string) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM users WHERE handle = $1 AND deleted_at IS NULL)`
	var taken bool
	if err := r.pool.QueryRow(ctx, q, handle).Scan(&taken); err != nil {
		return false, fmt.Errorf("auth: check handle: %w", err)
	}
	return taken, nil
}

// UpdateProfile applies the non-nil fields of upd. COALESCE keeps the statement
// to one round trip regardless of which fields were supplied.
func (r *Repo) UpdateProfile(ctx context.Context, id uuid.UUID, upd ProfileUpdate) (User, error) {
	const q = `
		UPDATE users SET
			display_name    = COALESCE($2::text, display_name),
			bio             = CASE WHEN $3::boolean THEN $4::text ELSE bio END,
			avatar_media_id = CASE WHEN $5::boolean THEN $6::uuid ELSE avatar_media_id END
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING ` + userColumns

	var bio any
	if upd.Bio != nil {
		bio = nullString(*upd.Bio)
	}
	var avatar any
	if upd.AvatarID != nil && *upd.AvatarID != uuid.Nil {
		avatar = *upd.AvatarID
	}

	u, err := scanUser(r.pool.QueryRow(ctx, q, id, upd.DisplayName,
		upd.Bio != nil, bio, upd.AvatarID != nil, avatar))
	if err != nil {
		return User{}, mapUserWriteError(err)
	}
	return u, nil
}

// UpdatePasswordHash replaces the stored hash, used for a transparent rehash
// after a successful login under outdated argon2 parameters.
func (r *Repo) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	const q = `UPDATE users SET password_hash = $2 WHERE id = $1 AND deleted_at IS NULL`
	if _, err := r.pool.Exec(ctx, q, id, hash); err != nil {
		return fmt.Errorf("auth: update password hash: %w", err)
	}
	return nil
}

// SetUserStatus moves an account between active and suspended. Deletion goes
// through SoftDeleteUser, which has side effects this does not.
func (r *Repo) SetUserStatus(ctx context.Context, id uuid.UUID, status string) error {
	const q = `UPDATE users SET status = $2 WHERE id = $1 AND deleted_at IS NULL`
	tag, err := r.pool.Exec(ctx, q, id, status)
	if err != nil {
		return fmt.Errorf("auth: set user status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SoftDeleteUser retires an account without dropping the row, which would take
// the user's recipes with it.
//
// handle is left in place: it is NOT NULL and CHECK-constrained, so it cannot
// be scrubbed. What frees it for reuse is deleted_at — the unique indexes on
// handle and email are partial on deleted_at IS NULL. email and password_hash
// are scrubbed because they are personal data with no reason to survive, and
// because a NULL email also steps out of the way of that partial index.
// Every refresh token dies in the same transaction, so sessions stop at once.
func (r *Repo) SoftDeleteUser(ctx context.Context, id uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("auth: begin delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		UPDATE users
		SET status = 'deleted', deleted_at = now(), email = NULL, password_hash = NULL
		WHERE id = $1 AND deleted_at IS NULL`
	tag, err := tx.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("auth: soft delete user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}

	const revoke = `
		UPDATE refresh_tokens SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL`
	if _, err := tx.Exec(ctx, revoke, id); err != nil {
		return fmt.Errorf("auth: revoke tokens on delete: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("auth: commit delete: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------- tokens --

const refreshColumns = `id, user_id, family_id, parent_id, token_hash,
	issued_at, expires_at, revoked_at, user_agent, ip`

func scanRefresh(row pgx.Row) (RefreshRecord, error) {
	var (
		rec       RefreshRecord
		parentID  *uuid.UUID
		revokedAt *time.Time
		userAgent *string
		ip        *netip.Addr
	)
	err := row.Scan(&rec.ID, &rec.UserID, &rec.FamilyID, &parentID, &rec.TokenHash,
		&rec.IssuedAt, &rec.ExpiresAt, &revokedAt, &userAgent, &ip)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefreshRecord{}, ErrRefreshNotFound
	}
	if err != nil {
		return RefreshRecord{}, fmt.Errorf("auth: scan refresh token: %w", err)
	}
	if parentID != nil {
		rec.ParentID = *parentID
	}
	if revokedAt != nil {
		rec.RevokedAt = *revokedAt
	}
	if userAgent != nil {
		rec.UserAgent = *userAgent
	}
	if ip != nil {
		rec.IP = ip.String()
	}
	return rec, nil
}

const insertRefreshSQL = `
	INSERT INTO refresh_tokens
		(id, user_id, family_id, parent_id, token_hash, issued_at, expires_at, user_agent, ip)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

// insertRefreshArgs flattens a record into the placeholder order above.
func insertRefreshArgs(rec RefreshRecord) []any {
	var parent any
	if rec.ParentID != uuid.Nil {
		parent = rec.ParentID
	}
	return []any{rec.ID, rec.UserID, rec.FamilyID, parent, rec.TokenHash,
		rec.IssuedAt, rec.ExpiresAt, nullString(rec.UserAgent), nullIP(rec.IP)}
}

// InsertRefreshToken stores a newly minted token.
func (r *Repo) InsertRefreshToken(ctx context.Context, rec RefreshRecord) error {
	if _, err := r.pool.Exec(ctx, insertRefreshSQL, insertRefreshArgs(rec)...); err != nil {
		return fmt.Errorf("auth: insert refresh token: %w", err)
	}
	return nil
}

// FindRefreshToken looks a token up by its hash.
//
// Revoked rows are returned rather than filtered out: rotation retires rows
// instead of deleting them precisely so that a replayed token can still be
// recognised as one that used to be ours.
func (r *Repo) FindRefreshToken(ctx context.Context, hash []byte) (RefreshRecord, error) {
	const q = `SELECT ` + refreshColumns + ` FROM refresh_tokens WHERE token_hash = $1`
	return scanRefresh(r.pool.QueryRow(ctx, q, hash))
}

// RotateRefreshToken retires the presented token and stores its successor in
// one transaction, so the two can never disagree.
func (r *Repo) RotateRefreshToken(ctx context.Context, oldID uuid.UUID, next RefreshRecord) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("auth: begin rotate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const revoke = `UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`
	tag, err := tx.Exec(ctx, revoke, oldID)
	if err != nil {
		return fmt.Errorf("auth: revoke rotated token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Another request rotated this same token between our read and our
		// write. Only one of them may win, and it was not this one.
		return ErrRefreshReuse
	}
	if _, err := tx.Exec(ctx, insertRefreshSQL, insertRefreshArgs(next)...); err != nil {
		return fmt.Errorf("auth: insert rotated token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("auth: commit rotate: %w", err)
	}
	return nil
}

// RevokeRefreshToken retires one token.
func (r *Repo) RevokeRefreshToken(ctx context.Context, id uuid.UUID) error {
	const q = `UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`
	if _, err := r.pool.Exec(ctx, q, id); err != nil {
		return fmt.Errorf("auth: revoke refresh token: %w", err)
	}
	return nil
}

// RevokeFamily retires every live token in one chain.
func (r *Repo) RevokeFamily(ctx context.Context, familyID uuid.UUID) (int64, error) {
	const q = `UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`
	tag, err := r.pool.Exec(ctx, q, familyID)
	if err != nil {
		return 0, fmt.Errorf("auth: revoke token family: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RevokeAllForUser retires every live token the user holds, across all devices.
func (r *Repo) RevokeAllForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	const q = `UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`
	tag, err := r.pool.Exec(ctx, q, userID)
	if err != nil {
		return 0, fmt.Errorf("auth: revoke user tokens: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ------------------------------------------------------ external identity --

// ExternalIdentity is one row of external_identities.
type ExternalIdentity struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Provider  string
	Subject   string
	Email     string
	CreatedAt time.Time
}

// FindUserByExternalIdentity resolves a provider subject to a live user.
func (r *Repo) FindUserByExternalIdentity(ctx context.Context, provider, subject string) (User, error) {
	q := `
		SELECT ` + userColumnsJoined + `
		FROM external_identities ei
		JOIN users u ON u.id = ei.user_id
		WHERE ei.provider = $1 AND ei.subject = $2 AND u.deleted_at IS NULL`
	return scanUser(r.pool.QueryRow(ctx, q, provider, subject))
}

// LinkExternalIdentity attaches a provider subject to a user, refreshing the
// recorded email if the provider now reports a different one.
//
// The upsert targets UNIQUE(provider, subject) and does not move an existing
// link to a different user: a subject that already belongs to someone else
// stays there, and the caller is told so rather than silently taking it over.
func (r *Repo) LinkExternalIdentity(ctx context.Context, userID uuid.UUID, provider, subject, email string) error {
	const q = `
		INSERT INTO external_identities (user_id, provider, subject, email)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, subject) DO UPDATE
			SET email = COALESCE(EXCLUDED.email, external_identities.email)
			WHERE external_identities.user_id = EXCLUDED.user_id
		RETURNING id`
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, q, userID, provider, subject, nullString(email)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.Conflict("Этот Apple ID уже привязан к другому аккаунту")
	}
	if err != nil {
		return fmt.Errorf("auth: link external identity: %w", err)
	}
	return nil
}

// ------------------------------------------------------------- utilities --

// mapUserWriteError turns a unique-index violation into a 409 that names the
// field the user has to change.
func mapUserWriteError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgUniqueViolation {
		return err
	}
	switch pgErr.ConstraintName {
	case "users_handle_active_key":
		return httpx.Conflict("Этот логин уже занят").WithCause(err)
	case "users_email_active_key":
		return httpx.Conflict("Этот e-mail уже зарегистрирован").WithCause(err)
	default:
		return httpx.Conflict("Такая запись уже существует").WithCause(err)
	}
}

// nullString maps "" to a SQL NULL, which is what the schema expects for an
// absent email or password.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullIP parses a client address for the inet column. An address we cannot
// parse is stored as NULL: provenance is a nice-to-have and must never be the
// reason a login fails.
func nullIP(s string) any {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return nil
	}
	return addr
}

// prefixed qualifies a column list with a table alias so the shared projection
// can be reused inside a join.
func prefixed(columns, alias string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
