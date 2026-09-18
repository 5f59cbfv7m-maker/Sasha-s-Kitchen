package moderation

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Content kinds a moderator acts on. Recipes and posts run the same workflow,
// so the actions take a kind rather than existing twice.
const (
	KindRecipe = "recipe"
	KindPost   = "post"
)

func tableFor(kind string) (string, error) {
	switch kind {
	case KindRecipe:
		return "recipes", nil
	case KindPost:
		return "posts", nil
	default:
		return "", fmt.Errorf("%w: unknown content kind %q", ErrBadState, kind)
	}
}

// Item is one piece of content in the moderator's work list.
type Item struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	Title        string `json:"title"`
	AuthorID     string `json:"author_id"`
	AuthorHandle string `json:"author_handle"`
	AuthorTrust  int    `json:"author_trust"`
	SubmittedAt  string `json:"submitted_at"`
	OpenReports  int    `json:"open_reports"`
}

// Pending lists everything waiting for review, oldest first.
//
// Both halves are backed by their review-queue indexes, which are declared
// WHERE status = 'review' precisely because this is a small, hot slice of two
// otherwise large tables.
func (r *Repo) Pending(ctx context.Context, limit int) ([]Item, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
		SELECT kind, id, title, author_id, handle, trust_level, submitted_at, open_reports
		  FROM (
		    SELECT 'recipe' AS kind, r.id::text AS id, r.title, u.id::text AS author_id,
		           u.handle::text AS handle, u.trust_level,
		           to_char(r.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS submitted_at,
		           r.updated_at AS sort_at,
		           (SELECT count(*) FROM reports rep
		             WHERE rep.target_type = 'recipe' AND rep.target_id = r.id
		               AND rep.status IN ('open','reviewing'))::int AS open_reports
		      FROM recipes r JOIN users u ON u.id = r.author_id
		     WHERE r.status = 'review' AND r.deleted_at IS NULL
		    UNION ALL
		    SELECT 'post', p.id::text, p.title, u.id::text,
		           u.handle::text, u.trust_level,
		           to_char(p.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		           p.updated_at,
		           (SELECT count(*) FROM reports rep
		             WHERE rep.target_type = 'post' AND rep.target_id = p.id
		               AND rep.status IN ('open','reviewing'))::int
		      FROM posts p JOIN users u ON u.id = p.author_id
		     WHERE p.status = 'review' AND p.deleted_at IS NULL
		  ) q
		 ORDER BY open_reports DESC, sort_at ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("moderation: pending: %w", err)
	}
	defer rows.Close()

	out := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.Kind, &it.ID, &it.Title, &it.AuthorID,
			&it.AuthorHandle, &it.AuthorTrust, &it.SubmittedAt, &it.OpenReports); err != nil {
			return nil, fmt.Errorf("moderation: scan pending: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ApproveContent publishes a reviewed recipe or post and counts the approval
// towards the author's promotion out of premoderation.
//
// published_at is set only on the first approval: something re-approved after
// an edit must keep its original date, or a fixed typo would send it back to
// the top of the "new" shelf.
func (r *Repo) ApproveContent(ctx context.Context, kind, id, moderatorID string) error {
	table, err := tableFor(kind)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("moderation: approve begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// table is one of two literals chosen above, never caller input.
	var authorID string
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		UPDATE %s SET status = 'published',
		              published_at = coalesce(published_at, now()),
		              rejected_reason = NULL
		 WHERE id = $1::uuid AND deleted_at IS NULL AND status = 'review'
		 RETURNING author_id::text`, table), id).Scan(&authorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBadState
	}
	if err != nil {
		return fmt.Errorf("moderation: approve %s: %w", kind, err)
	}

	if err := recordApproval(ctx, tx, authorID); err != nil {
		return err
	}
	if err := bumpPublishedCount(ctx, tx, kind, authorID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO moderation_events (target_type, target_id, user_id, actor_id, action)
		VALUES ($1, $2::uuid, $3::uuid, $4::uuid, 'approved')`,
		kind, id, authorID, moderatorID); err != nil {
		return fmt.Errorf("moderation: log approval: %w", err)
	}
	return tx.Commit(ctx)
}

// RejectContent sends something back to its author with a reason.
func (r *Repo) RejectContent(ctx context.Context, kind, id, moderatorID, reason string) error {
	table, err := tableFor(kind)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("moderation: reject begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var authorID string
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		UPDATE %s SET status = 'rejected', rejected_reason = $2
		 WHERE id = $1::uuid AND deleted_at IS NULL AND status = 'review'
		 RETURNING author_id::text`, table), id, reason).Scan(&authorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBadState
	}
	if err != nil {
		return fmt.Errorf("moderation: reject %s: %w", kind, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO moderation_events (target_type, target_id, user_id, actor_id, action, note)
		VALUES ($1, $2::uuid, $3::uuid, $4::uuid, 'rejected', nullif($5,''))`,
		kind, id, authorID, moderatorID, reason); err != nil {
		return fmt.Errorf("moderation: log rejection: %w", err)
	}
	return tx.Commit(ctx)
}

// TakedownContent removes something already live, resolves the complaints about
// it, and drops the author back to premoderation.
//
// The demotion is the part that makes "publish first, moderate on report"
// survivable: without it, an author who has been caught once carries on
// publishing straight to the storefront.
func (r *Repo) TakedownContent(ctx context.Context, kind, id, moderatorID, reason string) error {
	table, err := tableFor(kind)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("moderation: takedown begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var authorID string
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		UPDATE %s SET status = 'archived', rejected_reason = $2
		 WHERE id = $1::uuid AND deleted_at IS NULL AND status = 'published'
		 RETURNING author_id::text`, table), id, reason).Scan(&authorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBadState
	}
	if err != nil {
		return fmt.Errorf("moderation: takedown %s: %w", kind, err)
	}

	// Taken-down content must not still be sitting in someone's work list.
	if _, err := tx.Exec(ctx, `
		UPDATE reports SET status = 'upheld', resolver_id = $3::uuid, resolved_at = now()
		 WHERE target_type = $1 AND target_id = $2::uuid AND status IN ('open','reviewing')`,
		kind, id, moderatorID); err != nil {
		return fmt.Errorf("moderation: resolve reports: %w", err)
	}
	if err := demote(ctx, tx, authorID); err != nil {
		return err
	}
	if err := dropPublishedCount(ctx, tx, kind, authorID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO moderation_events (target_type, target_id, user_id, actor_id, action, note)
		VALUES ($1, $2::uuid, $3::uuid, $4::uuid, 'takedown', nullif($5,''))`,
		kind, id, authorID, moderatorID, reason); err != nil {
		return fmt.Errorf("moderation: log takedown: %w", err)
	}
	return tx.Commit(ctx)
}

func bumpPublishedCount(ctx context.Context, tx pgx.Tx, kind, authorID string) error {
	col := "recipe_count"
	if kind == KindPost {
		col = "post_count"
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		`UPDATE user_stats SET %s = %s + 1 WHERE user_id = $1::uuid`, col, col), authorID); err != nil {
		return fmt.Errorf("moderation: bump %s: %w", col, err)
	}
	return nil
}

func dropPublishedCount(ctx context.Context, tx pgx.Tx, kind, authorID string) error {
	col := "recipe_count"
	if kind == KindPost {
		col = "post_count"
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		`UPDATE user_stats SET %s = greatest(%s - 1, 0) WHERE user_id = $1::uuid`, col, col),
		authorID); err != nil {
		return fmt.Errorf("moderation: drop %s: %w", col, err)
	}
	return nil
}

// Report is one open complaint as a moderator sees it.
type Report struct {
	ID             string `json:"id"`
	TargetType     string `json:"target_type"`
	TargetID       string `json:"target_id"`
	TargetTitle    string `json:"target_title,omitempty"`
	Reason         string `json:"reason"`
	Details        string `json:"details,omitempty"`
	ReporterHandle string `json:"reporter_handle,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// OpenReports lists unresolved complaints, oldest first, so the queue is
// answered in the order people raised it.
//
// The title is resolved per target type here rather than left to the client:
// a moderator deciding on a complaint needs to see what it is about without a
// second request per row.
func (r *Repo) OpenReports(ctx context.Context, limit int) ([]Report, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
		SELECT rep.id::text, rep.target_type, rep.target_id::text,
		       coalesce(
		           (SELECT title FROM recipes WHERE id = rep.target_id AND rep.target_type = 'recipe'),
		           (SELECT title FROM posts   WHERE id = rep.target_id AND rep.target_type = 'post'),
		           (SELECT display_name FROM users WHERE id = rep.target_id AND rep.target_type = 'user'),
		           ''),
		       rep.reason, coalesce(rep.details, ''),
		       coalesce(u.handle::text, ''),
		       to_char(rep.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		  FROM reports rep
		  LEFT JOIN users u ON u.id = rep.reporter_id
		 WHERE rep.status IN ('open', 'reviewing')
		 ORDER BY rep.created_at ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("moderation: open reports: %w", err)
	}
	defer rows.Close()

	out := []Report{}
	for rows.Next() {
		var rep Report
		if err := rows.Scan(&rep.ID, &rep.TargetType, &rep.TargetID, &rep.TargetTitle,
			&rep.Reason, &rep.Details, &rep.ReporterHandle, &rep.CreatedAt); err != nil {
			return nil, fmt.Errorf("moderation: scan report: %w", err)
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}
