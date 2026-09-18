// Package moderation implements the publish workflow and the user-generated
// content safety surface.
//
// App Store Guideline 1.2 requires a UGC app to offer a way to report content,
// a way to block an author, and a moderator able to take content down. None of
// that is optional, so it ships with the first version rather than being
// retrofitted after a rejection.
package moderation

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound covers both "absent" and "not yours", deliberately: telling a
	// caller that a recipe exists but belongs to someone else leaks the catalogue.
	ErrNotFound = errors.New("moderation: not found")
	// ErrDuplicate is returned when an open report already exists.
	ErrDuplicate = errors.New("moderation: already reported")
	// ErrBadState is returned for an illegal workflow transition.
	ErrBadState = errors.New("moderation: illegal transition")
)

// Repo owns moderation state.
type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// ReportInput is a user's complaint about content.
type ReportInput struct {
	TargetType string `json:"target_type"` // recipe | user | rating
	TargetID   string `json:"target_id"`
	Reason     string `json:"reason"`
	Details    string `json:"details,omitempty"`
}

// Report files a complaint. The partial unique index in the schema enforces one
// open report per reporter per target, so a user tapping repeatedly cannot
// flood the queue; that collision surfaces here as ErrDuplicate.
func (r *Repo) Report(ctx context.Context, reporterID string, in ReportInput) (string, error) {
	exists, err := r.targetExists(ctx, in.TargetType, in.TargetID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", ErrNotFound
	}

	var id string
	err = r.pool.QueryRow(ctx, `
		INSERT INTO reports (reporter_id, target_type, target_id, reason, details)
		VALUES ($1::uuid, $2, $3::uuid, $4, nullif($5,''))
		RETURNING id::text`,
		reporterID, in.TargetType, in.TargetID, in.Reason, in.Details).Scan(&id)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return "", ErrDuplicate
	}
	if err != nil {
		return "", fmt.Errorf("moderation: file report: %w", err)
	}
	return id, nil
}

// targetExists reports whether the thing being reported is real.
//
// reports.target_id is polymorphic and therefore carries no foreign key, so
// the database cannot reject a complaint about a UUID that was never issued.
// Without this check the moderation queue accepts junk that a human then has
// to work through. Report calls it before inserting.
func (r *Repo) targetExists(ctx context.Context, targetType, targetID string) (bool, error) {
	var q string
	switch targetType {
	case "recipe":
		q = `SELECT EXISTS (SELECT 1 FROM recipes WHERE id = $1::uuid AND deleted_at IS NULL)`
	case "user":
		q = `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1::uuid AND deleted_at IS NULL)`
	case "rating":
		// ratings is keyed by (user_id, recipe_id) and has no id of its own, so
		// a rating is reported by the id of the recipe it sits on.
		q = `SELECT EXISTS (SELECT 1 FROM ratings WHERE recipe_id = $1::uuid)`
	default:
		return false, nil
	}
	var ok bool
	if err := r.pool.QueryRow(ctx, q, targetID).Scan(&ok); err != nil {
		return false, fmt.Errorf("moderation: check report target: %w", err)
	}
	return ok, nil
}

// BlockedUser is one entry of a viewer's block list.
type BlockedUser struct {
	ID          string
	Handle      string
	DisplayName string
	BlockedAt   string
}

// ListBlocked returns who this viewer has blocked, newest first. A block a
// user cannot find again is a trap rather than a control, and Guideline 1.2
// expects the list to be manageable from inside the app.
func (r *Repo) ListBlocked(ctx context.Context, blockerID string) ([]BlockedUser, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.id::text, u.handle::text, u.display_name,
		       to_char(b.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		  FROM user_blocks b
		  JOIN users u ON u.id = b.blocked_id
		 WHERE b.blocker_id = $1::uuid AND u.deleted_at IS NULL
		 ORDER BY b.created_at DESC, u.id DESC`, blockerID)
	if err != nil {
		return nil, fmt.Errorf("moderation: list blocked: %w", err)
	}
	defer rows.Close()

	out := []BlockedUser{}
	for rows.Next() {
		var b BlockedUser
		if err := rows.Scan(&b.ID, &b.Handle, &b.DisplayName, &b.BlockedAt); err != nil {
			return nil, fmt.Errorf("moderation: scan blocked: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Block hides an author's content from one viewer. Blocking is per-viewer and
// takes effect immediately, because the storefront query filters on it.
func (r *Repo) Block(ctx context.Context, blockerID, blockedID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO user_blocks (blocker_id, blocked_id)
		VALUES ($1::uuid, $2::uuid)
		ON CONFLICT DO NOTHING`, blockerID, blockedID)
	if err != nil {
		return fmt.Errorf("moderation: block: %w", err)
	}
	return nil
}

// Unblock reverses Block.
func (r *Repo) Unblock(ctx context.Context, blockerID, blockedID string) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM user_blocks WHERE blocker_id = $1::uuid AND blocked_id = $2::uuid`,
		blockerID, blockedID)
	if err != nil {
		return fmt.Errorf("moderation: unblock: %w", err)
	}
	return nil
}

// Submit moves an author's draft into the review queue.
//
// Nothing reaches the storefront without passing through here: publishing is a
// moderator action, never an author one.
func (r *Repo) Submit(ctx context.Context, recipeID, authorID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE recipes SET status = 'review'
		 WHERE id = $1::uuid AND author_id = $2::uuid
		   AND deleted_at IS NULL
		   AND status IN ('draft', 'rejected')`, recipeID, authorID)
	if err != nil {
		return fmt.Errorf("moderation: submit: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return r.logEvent(ctx, recipeID, authorID, "submitted", "")
}

// QueueItem is one row of the moderator's work list.
type QueueItem struct {
	RecipeID     string `json:"recipe_id"`
	Title        string `json:"title"`
	AuthorHandle string `json:"author_handle"`
	SubmittedAt  string `json:"submitted_at"`
	OpenReports  int    `json:"open_reports"`
}

// Queue lists recipes awaiting review, oldest first so nothing starves.
func (r *Repo) Queue(ctx context.Context, limit int) ([]QueueItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.id::text, r.title, u.handle,
		       to_char(r.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       (SELECT count(*) FROM reports rep
		         WHERE rep.target_type = 'recipe' AND rep.target_id = r.id
		           AND rep.status IN ('open','reviewing'))::int
		  FROM recipes r
		  JOIN users u ON u.id = r.author_id
		 WHERE r.status = 'review' AND r.deleted_at IS NULL
		 ORDER BY r.updated_at ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("moderation: queue: %w", err)
	}
	defer rows.Close()

	out := []QueueItem{}
	for rows.Next() {
		var it QueueItem
		if err := rows.Scan(&it.RecipeID, &it.Title, &it.AuthorHandle,
			&it.SubmittedAt, &it.OpenReports); err != nil {
			return nil, fmt.Errorf("moderation: scan queue: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Approve publishes a reviewed recipe.
//
// published_at is set only on the first approval: a recipe re-approved after an
// edit must keep its original date, or it would jump back to the top of the
// "new" shelf every time a typo is fixed.
func (r *Repo) Approve(ctx context.Context, recipeID, moderatorID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE recipes
		   SET status = 'published',
		       published_at = coalesce(published_at, now()),
		       rejected_reason = NULL
		 WHERE id = $1::uuid AND deleted_at IS NULL AND status = 'review'`, recipeID)
	if err != nil {
		return fmt.Errorf("moderation: approve: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBadState
	}
	return r.logEvent(ctx, recipeID, moderatorID, "approved", "")
}

// Reject sends a recipe back to its author with a reason.
func (r *Repo) Reject(ctx context.Context, recipeID, moderatorID, reason string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE recipes SET status = 'rejected', rejected_reason = $2
		 WHERE id = $1::uuid AND deleted_at IS NULL AND status = 'review'`,
		recipeID, reason)
	if err != nil {
		return fmt.Errorf("moderation: reject: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBadState
	}
	return r.logEvent(ctx, recipeID, moderatorID, "rejected", reason)
}

// Takedown removes already-published content. Unlike Reject it applies to a
// live recipe, which is what a upheld report needs.
func (r *Repo) Takedown(ctx context.Context, recipeID, moderatorID, reason string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("moderation: takedown begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	tag, err := tx.Exec(ctx, `
		UPDATE recipes SET status = 'archived', rejected_reason = $2
		 WHERE id = $1::uuid AND deleted_at IS NULL AND status = 'published'`,
		recipeID, reason)
	if err != nil {
		return fmt.Errorf("moderation: takedown: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBadState
	}
	// Resolving the reports in the same transaction keeps the queue honest: a
	// taken-down recipe must not still be sitting in someone's work list.
	if _, err := tx.Exec(ctx, `
		UPDATE reports SET status = 'upheld', resolver_id = $2::uuid, resolved_at = now()
		 WHERE target_type = 'recipe' AND target_id = $1::uuid
		   AND status IN ('open','reviewing')`, recipeID, moderatorID); err != nil {
		return fmt.Errorf("moderation: resolve reports: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO moderation_events (recipe_id, actor_id, action, note)
		VALUES ($1::uuid, $2::uuid, 'takedown', nullif($3,''))`,
		recipeID, moderatorID, reason); err != nil {
		return fmt.Errorf("moderation: log takedown: %w", err)
	}
	return tx.Commit(ctx)
}

// Suspend disables an account. The storefront joins on users.status, so this
// removes the author's entire catalogue at once without touching each recipe.
func (r *Repo) Suspend(ctx context.Context, userID, moderatorID, reason string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("moderation: suspend begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	tag, err := tx.Exec(ctx,
		`UPDATE users SET status = 'suspended' WHERE id = $1::uuid AND status = 'active'`,
		userID)
	if err != nil {
		return fmt.Errorf("moderation: suspend: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBadState
	}
	// Existing sessions must stop working immediately, not at token expiry.
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now()
		  WHERE user_id = $1::uuid AND revoked_at IS NULL`, userID); err != nil {
		return fmt.Errorf("moderation: revoke sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO moderation_events (user_id, actor_id, action, note)
		VALUES ($1::uuid, $2::uuid, 'suspended', nullif($3,''))`,
		userID, moderatorID, reason); err != nil {
		return fmt.Errorf("moderation: log suspend: %w", err)
	}
	return tx.Commit(ctx)
}

// ResolveReport closes a report without acting on the content.
func (r *Repo) ResolveReport(ctx context.Context, reportID, moderatorID, status, note string) error {
	if status != "upheld" && status != "dismissed" {
		return ErrBadState
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE reports
		   SET status = $2, resolver_id = $3::uuid, resolution = nullif($4,''), resolved_at = now()
		 WHERE id = $1::uuid AND status IN ('open','reviewing')`,
		reportID, status, moderatorID, note)
	if err != nil {
		return fmt.Errorf("moderation: resolve: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repo) logEvent(ctx context.Context, recipeID, actorID, action, note string) error {
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO moderation_events (recipe_id, actor_id, action, note)
		VALUES ($1::uuid, nullif($2,'')::uuid, $3, nullif($4,''))`,
		recipeID, actorID, action, note); err != nil {
		return fmt.Errorf("moderation: log event: %w", err)
	}
	return nil
}

// ensure pgx is referenced for error helpers used by callers of this package.
var _ = pgx.ErrNoRows
