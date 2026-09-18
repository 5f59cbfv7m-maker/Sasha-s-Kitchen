package moderation

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PromoteAfter is how many approved publications lift a newcomer out of
// premoderation.
//
// Three is a judgement, not a law: enough for a moderator to see whether
// someone understands the rules, few enough that a real cook is not stuck in a
// queue for a week. It lives here rather than in the database because changing
// it does not need to rewrite anyone's level.
const PromoteAfter = 3

// Trust levels, mirroring the trust_levels table and the users.trust_level
// CHECK constraint.
const (
	TrustRestricted = -1
	TrustNewcomer   = 0
	TrustTrusted    = 1
	TrustVerified   = 2
)

// recordApproval counts an approval towards promotion and lifts the author out
// of premoderation once they have enough.
//
// A verified author is never touched: verification is a human decision and an
// automatic rule must not quietly undo it.
func recordApproval(ctx context.Context, tx pgx.Tx, authorID string) error {
	var approved int
	if err := tx.QueryRow(ctx, `
		UPDATE user_stats SET approved_count = approved_count + 1
		 WHERE user_id = $1::uuid
		 RETURNING approved_count`, authorID).Scan(&approved); err != nil {
		return fmt.Errorf("moderation: count approval: %w", err)
	}
	if approved < PromoteAfter {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET trust_level = $2
		 WHERE id = $1::uuid AND trust_level = $3`,
		authorID, TrustTrusted, TrustNewcomer); err != nil {
		return fmt.Errorf("moderation: promote author: %w", err)
	}
	return nil
}

// demote drops an author back to premoderation after an upheld complaint and
// resets their progress, so trust has to be earned again rather than resumed
// from where it was when the violation happened.
func demote(ctx context.Context, tx pgx.Tx, authorID string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE users SET trust_level = $2
		 WHERE id = $1::uuid AND trust_level > $2 AND verified_at IS NULL`,
		authorID, TrustRestricted); err != nil {
		return fmt.Errorf("moderation: demote author: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE user_stats SET approved_count = 0 WHERE user_id = $1::uuid`, authorID); err != nil {
		return fmt.Errorf("moderation: reset approvals: %w", err)
	}
	return nil
}

// SetVerified marks or unmarks an author as verified, which is a human
// decision: it is the badge that distinguishes a real cook from a copycat
// account, so nothing automatic may grant it.
func (r *Repo) SetVerified(ctx context.Context, userID, moderatorID string, verified bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("moderation: verify begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	level := TrustVerified
	action := "verified"
	if !verified {
		// Unverifying returns the author to the trusted tier rather than to
		// premoderation: losing a badge is not a rule violation.
		level = TrustTrusted
		action = "unverified"
	}
	tag, err := tx.Exec(ctx, `
		UPDATE users
		   SET verified_at = CASE WHEN $2 THEN coalesce(verified_at, now()) ELSE NULL END,
		       trust_level = $3
		 WHERE id = $1::uuid AND deleted_at IS NULL`, userID, verified, int16(level))
	if err != nil {
		return fmt.Errorf("moderation: set verified: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO moderation_events (target_type, target_id, user_id, actor_id, action, note)
		VALUES ('user', $1::uuid, $1::uuid, $2::uuid, 'suspended', $3)`,
		userID, moderatorID, action); err != nil {
		// 'verified' is not in the moderation_events action CHECK; the note
		// carries it instead of widening an enum for a bookkeeping entry.
		return fmt.Errorf("moderation: log verification: %w", err)
	}
	return tx.Commit(ctx)
}

// TrustOf reports an author's current level, for tests and admin views.
func (r *Repo) TrustOf(ctx context.Context, userID string) (int, int, error) {
	var level, approved int
	err := r.pool.QueryRow(ctx, `
		SELECT u.trust_level, s.approved_count
		  FROM users u JOIN user_stats s ON s.user_id = u.id
		 WHERE u.id = $1::uuid`, userID).Scan(&level, &approved)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrNotFound
	}
	if err != nil {
		return 0, 0, fmt.Errorf("moderation: read trust: %w", err)
	}
	return level, approved, nil
}
