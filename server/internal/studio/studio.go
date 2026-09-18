// Package studio is the authenticated surface an author uses to run their own
// page: edit the profile, write recipes and posts, and publish them.
//
// Before this package there was no way to create a recipe at all. The publish
// workflow existed -- moderation.Submit moved a draft into review -- but
// nothing could produce the draft, so the store could only ever show rows
// inserted by hand.
package studio

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/moderation"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/posts"
)

var (
	// ErrNotFound covers "no such draft" and "not yours" together: telling a
	// caller that someone else's draft exists leaks the id space.
	ErrNotFound = errors.New("studio: not found")
	// ErrQuota is returned when an author has published as much as their trust
	// level allows today.
	ErrQuota = errors.New("studio: daily quota reached")
	// ErrBadState is an illegal workflow transition, such as submitting
	// something already published.
	ErrBadState = errors.New("studio: illegal transition")
)

// TrustPolicy is one row of the trust_levels table: what an author at this
// level may do in a day.
type TrustPolicy struct {
	Level             int
	Title             string
	Premoderate       bool
	DailyPublications int
	MaxVideoSeconds   int
	MayShowLinks      bool
}

// Repo owns the author's own content.
type Repo struct {
	pool  *pgxpool.Pool
	posts *posts.Repo
	mod   *moderation.Repo
}

func NewRepo(pool *pgxpool.Pool, p *posts.Repo, mod *moderation.Repo) *Repo {
	return &Repo{pool: pool, posts: p, mod: mod}
}

// Policy reads the quota row for an author's current trust level.
func (r *Repo) Policy(ctx context.Context, authorID string) (TrustPolicy, error) {
	var p TrustPolicy
	err := r.pool.QueryRow(ctx, `
		SELECT t.level, t.title, t.premoderate, t.daily_publications,
		       t.max_video_seconds, t.may_show_links
		  FROM users u
		  JOIN trust_levels t ON t.level = u.trust_level
		 WHERE u.id = $1::uuid AND u.deleted_at IS NULL AND u.status = 'active'`,
		authorID).Scan(&p.Level, &p.Title, &p.Premoderate, &p.DailyPublications,
		&p.MaxVideoSeconds, &p.MayShowLinks)
	if errors.Is(err, pgx.ErrNoRows) {
		return TrustPolicy{}, ErrNotFound
	}
	if err != nil {
		return TrustPolicy{}, fmt.Errorf("studio: read trust policy: %w", err)
	}
	return p, nil
}

// publishGate decides what happens when an author submits something, and
// enforces the daily quota.
//
// It runs inside the caller's transaction and takes a row lock on the author's
// stats row first. Two submissions racing would otherwise both read "2 today"
// under a limit of 3 and both be allowed; locking one row per author serialises
// exactly the authors who are publishing and nobody else.
func (r *Repo) publishGate(ctx context.Context, tx pgx.Tx, authorID string) (TrustPolicy, error) {
	if _, err := tx.Exec(ctx,
		`SELECT 1 FROM user_stats WHERE user_id = $1::uuid FOR UPDATE`, authorID); err != nil {
		return TrustPolicy{}, fmt.Errorf("studio: lock author stats: %w", err)
	}

	var p TrustPolicy
	if err := tx.QueryRow(ctx, `
		SELECT t.level, t.title, t.premoderate, t.daily_publications,
		       t.max_video_seconds, t.may_show_links
		  FROM users u
		  JOIN trust_levels t ON t.level = u.trust_level
		 WHERE u.id = $1::uuid AND u.deleted_at IS NULL AND u.status = 'active'`,
		authorID).Scan(&p.Level, &p.Title, &p.Premoderate, &p.DailyPublications,
		&p.MaxVideoSeconds, &p.MayShowLinks); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TrustPolicy{}, ErrNotFound
		}
		return TrustPolicy{}, fmt.Errorf("studio: read trust policy: %w", err)
	}

	// Count transitions, not rows: a draft sent to review, pulled back and sent
	// again is one row and two publications.
	var today int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM moderation_events
		 WHERE user_id = $1::uuid AND action = 'submitted'
		   AND created_at > now() - interval '24 hours'`, authorID).Scan(&today); err != nil {
		return TrustPolicy{}, fmt.Errorf("studio: count today's publications: %w", err)
	}
	if today >= p.DailyPublications {
		return p, ErrQuota
	}
	return p, nil
}

// Stats is the author's dashboard.
type Stats struct {
	TrustLevel        int    `json:"trust_level"`
	TrustTitle        string `json:"trust_title"`
	Premoderated      bool   `json:"premoderated"`
	DailyLimit        int    `json:"daily_limit"`
	PublishedToday    int    `json:"published_today"`
	MaxVideoSeconds   int    `json:"max_video_seconds"`
	Recipes           Counts `json:"recipes"`
	Posts             Counts `json:"posts"`
	Followers         int    `json:"followers"`
	Imports           int64  `json:"imports"`
	PendingModeration int    `json:"pending_moderation"`
}

// Counts breaks content down by workflow state, which is what an author
// actually wants to see: what is live, what is waiting, what came back.
type Counts struct {
	Draft     int `json:"draft"`
	Review    int `json:"review"`
	Published int `json:"published"`
	Rejected  int `json:"rejected"`
}

func (r *Repo) Stats(ctx context.Context, authorID string) (*Stats, error) {
	p, err := r.Policy(ctx, authorID)
	if err != nil {
		return nil, err
	}
	s := &Stats{
		TrustLevel: p.Level, TrustTitle: p.Title, Premoderated: p.Premoderate,
		DailyLimit: p.DailyPublications, MaxVideoSeconds: p.MaxVideoSeconds,
	}

	if err := r.pool.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE status = 'draft'),
		  count(*) FILTER (WHERE status = 'review'),
		  count(*) FILTER (WHERE status = 'published'),
		  count(*) FILTER (WHERE status = 'rejected')
		  FROM recipes WHERE author_id = $1::uuid AND deleted_at IS NULL`,
		authorID).Scan(&s.Recipes.Draft, &s.Recipes.Review,
		&s.Recipes.Published, &s.Recipes.Rejected); err != nil {
		return nil, fmt.Errorf("studio: recipe counts: %w", err)
	}
	if err := r.pool.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE status = 'draft'),
		  count(*) FILTER (WHERE status = 'review'),
		  count(*) FILTER (WHERE status = 'published'),
		  count(*) FILTER (WHERE status = 'rejected')
		  FROM posts WHERE author_id = $1::uuid AND deleted_at IS NULL`,
		authorID).Scan(&s.Posts.Draft, &s.Posts.Review,
		&s.Posts.Published, &s.Posts.Rejected); err != nil {
		return nil, fmt.Errorf("studio: post counts: %w", err)
	}
	if err := r.pool.QueryRow(ctx, `
		SELECT follower_count, import_total FROM user_stats WHERE user_id = $1::uuid`,
		authorID).Scan(&s.Followers, &s.Imports); err != nil {
		return nil, fmt.Errorf("studio: author stats: %w", err)
	}
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM moderation_events
		 WHERE user_id = $1::uuid AND action = 'submitted'
		   AND created_at > now() - interval '24 hours'`,
		authorID).Scan(&s.PublishedToday); err != nil {
		return nil, fmt.Errorf("studio: today's publications: %w", err)
	}
	s.PendingModeration = s.Recipes.Review + s.Posts.Review
	return s, nil
}

// publishOutcome names what happened to a submission, so the client can say
// "опубликовано" or "отправлено на проверку" rather than guessing.
type publishOutcome struct {
	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}
