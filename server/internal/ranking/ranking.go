// Package ranking computes the author leaderboard and the ranked shorts feed.
//
// Both are recomputed by a background job rather than read live. A leaderboard
// query over every author's imports and ratings is expensive and gets slower as
// the store grows; worse, a feed ordered by a value that moves while a reader
// pages skips and repeats cards, which on a vertical video feed is exactly the
// glitch people notice.
package ranking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/jobs"
)

// JobKind is the jobs.kind this package's handler answers to.
const JobKind = "ranking.refresh"

// WindowDays is the rolling window every term is measured over.
//
// A badge that can never be lost is a badge the earliest accounts keep
// forever, so nothing here counts lifetime totals.
const WindowDays = 30

// Scoring weights. Imports dominate because taking a recipe into your own
// kitchen is the strongest signal this store has that the recipe was good.
const (
	weightImports   = 0.55
	weightRating    = 0.30
	weightFollowers = 0.15
)

// bayesPrior is the weight of the store-wide average in an author's rating.
//
// Without it a single five-star review outranks a hundred four-star ones. With
// it, a small sample is pulled towards the middle until there is enough of it
// to mean something.
const bayesPrior = 20

// Eligibility gates. These do most of the anti-gaming work and cost nothing:
// a fake account cannot clear them without real readers.
const (
	minPublishedRecipes = 3
	minImports          = 10
	minAccountAgeDays   = 30
	// minImporterAgeDays ignores imports from accounts registered in the last
	// week. Counting distinct users alone does not stop someone creating
	// throwaways to import their own work; requiring the importer to predate
	// the campaign does.
	minImporterAgeDays = 7
)

// BadgeTop is how high an author must place to get the badge next to their
// avatar.
const BadgeTop = 3

// Repo computes and reads rankings.
type Repo struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewRepo(pool *pgxpool.Pool, logger *slog.Logger) *Repo {
	return &Repo{pool: pool, logger: logger}
}

// Handler adapts the refresh to the job runner.
func (r *Repo) Handler() jobs.Handler {
	return func(ctx context.Context, _ jobs.Job) error { return r.Refresh(ctx) }
}

// Refresh recomputes every ranking in one transaction.
//
// One transaction because a half-applied leaderboard is worse than a stale
// one: readers would see two authors claiming the same rank.
func (r *Repo) Refresh(ctx context.Context) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ranking: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Reconcile the counters that are deliberately not trigger-maintained.
	// Doing it here rather than on every import or rating means a popular
	// author's stats row is written once per refresh instead of once per event.
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		UPDATE user_stats s SET
		    follower_count = coalesce(f.n, 0),
		    import_total   = coalesce(i.n, 0),
		    rating_avg     = r.avg,
		    rating_count   = coalesce(r.n, 0),
		    recipe_count   = coalesce(rc.n, 0),
		    post_count     = coalesce(pc.n, 0),
		    short_count    = coalesce(sc.n, 0),
		    recomputed_at  = now()
		  FROM users u
		  LEFT JOIN LATERAL (SELECT count(*) AS n FROM follows WHERE author_id = u.id) f ON true
		  LEFT JOIN LATERAL (
		      SELECT count(DISTINCT ri.user_id) AS n
		        FROM recipe_imports ri JOIN recipes rr ON rr.id = ri.recipe_id
		       WHERE rr.author_id = u.id AND ri.user_id IS NOT NULL
		         AND ri.user_id <> u.id) i ON true
		  LEFT JOIN LATERAL (
		      SELECT avg(rt.score)::numeric(3,2) AS avg, count(*) AS n
		        FROM ratings rt JOIN recipes rr ON rr.id = rt.recipe_id
		       WHERE rr.author_id = u.id) r ON true
		  LEFT JOIN LATERAL (SELECT count(*) AS n FROM recipes
		       WHERE author_id = u.id AND status = 'published' AND deleted_at IS NULL) rc ON true
		  LEFT JOIN LATERAL (SELECT count(*) AS n FROM posts
		       WHERE author_id = u.id AND kind = 'article'
		         AND status = 'published' AND deleted_at IS NULL) pc ON true
		  LEFT JOIN LATERAL (SELECT count(*) AS n FROM posts
		       WHERE author_id = u.id AND kind = 'short'
		         AND status = 'published' AND deleted_at IS NULL) sc ON true
		 WHERE s.user_id = u.id`)); err != nil {
		return fmt.Errorf("ranking: reconcile stats: %w", err)
	}

	if err := r.refreshAuthors(ctx, tx); err != nil {
		return err
	}
	// Drop anyone this run did not rank: an author who stopped qualifying --
	// suspended, quiet for a month, or carrying an upheld complaint -- must
	// leave the board rather than keep a stale rank.
	//
	// now() is the transaction timestamp and is therefore identical to the
	// computed_at just written, so `<` matches exactly the rows this run did
	// not touch.
	if _, err := tx.Exec(ctx, `DELETE FROM author_ranks WHERE computed_at < now()`); err != nil {
		return fmt.Errorf("ranking: drop stale ranks: %w", err)
	}
	if err := r.refreshShorts(ctx, tx); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE ranking_state SET epoch = epoch + 1, computed_at = now() WHERE id`); err != nil {
		return fmt.Errorf("ranking: bump epoch: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ranking: commit: %w", err)
	}
	return nil
}

// refreshAuthors rebuilds the leaderboard.
//
// Every term is normalised against the window's own maximum before weighting,
// so the weights mean what they say. Mixing a raw logarithm with a 0..1 term
// would let whichever has the larger natural range quietly dominate.
func (r *Repo) refreshAuthors(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, fmt.Sprintf(`
WITH params AS (
    SELECT make_interval(days => %d) AS window,
           make_interval(days => %d) AS min_age,
           make_interval(days => %d) AS importer_age
),
-- Distinct importers, excluding the author themselves and throwaway accounts
-- registered after the work appeared.
imports AS (
    SELECT rr.author_id, count(DISTINCT ri.user_id)::int AS n
      FROM recipe_imports ri
      JOIN recipes rr ON rr.id = ri.recipe_id
      JOIN users iu ON iu.id = ri.user_id
     CROSS JOIN params p
     WHERE ri.created_at > now() - p.window
       AND ri.user_id IS NOT NULL
       AND ri.user_id <> rr.author_id
       AND iu.created_at < now() - p.importer_age
       AND iu.status = 'active'
     GROUP BY rr.author_id
),
ratings_w AS (
    SELECT rr.author_id, avg(rt.score)::numeric AS avg, count(*)::int AS n
      FROM ratings rt
      JOIN recipes rr ON rr.id = rt.recipe_id
     CROSS JOIN params p
     WHERE rt.created_at > now() - p.window
     GROUP BY rr.author_id
),
followers AS (
    SELECT f.author_id, count(*)::int AS n
      FROM follows f CROSS JOIN params p
     WHERE f.created_at > now() - p.window
     GROUP BY f.author_id
),
global AS (
    SELECT coalesce(avg(rt.score), 3.0)::numeric AS mean
      FROM ratings rt CROSS JOIN params p
     WHERE rt.created_at > now() - p.window
),
eligible AS (
    SELECT u.id AS user_id,
           coalesce(i.n, 0) AS imports_n,
           rw.avg           AS rating_avg,
           coalesce(rw.n, 0) AS rating_n,
           coalesce(fl.n, 0) AS followers_n
      FROM users u
      JOIN user_stats s ON s.user_id = u.id
      LEFT JOIN imports   i  ON i.author_id  = u.id
      LEFT JOIN ratings_w rw ON rw.author_id = u.id
      LEFT JOIN followers fl ON fl.author_id = u.id
     CROSS JOIN params p
     WHERE u.deleted_at IS NULL
       AND u.status = 'active'
       AND u.trust_level >= 1
       AND u.created_at < now() - p.min_age
       AND s.recipe_count >= %d
       AND coalesce(i.n, 0) >= %d
       -- An upheld complaint in the window disqualifies outright. This is the
       -- only mitigation there is for stolen content, and it is worth more
       -- than any weighting.
       AND NOT EXISTS (
           SELECT 1 FROM moderation_events me
            WHERE me.user_id = u.id AND me.action = 'takedown'
              AND me.created_at > now() - p.window)
),
maxima AS (
    SELECT greatest(max(imports_n), 1)   AS max_imports,
           greatest(max(followers_n), 1) AS max_followers
      FROM eligible
),
scored AS (
    SELECT e.user_id,
           (%f * ln(1 + e.imports_n) / ln(1 + m.max_imports)
          + %f * ((
                (e.rating_n::numeric / (e.rating_n + %d)) * coalesce(e.rating_avg, g.mean)
              + (%d::numeric / (e.rating_n + %d)) * g.mean
            ) - 1.0) / 4.0
          + %f * ln(1 + e.followers_n) / ln(1 + m.max_followers)
           )::numeric(10,6) AS score,
           e.imports_n, e.rating_avg, e.rating_n, e.followers_n
      FROM eligible e CROSS JOIN maxima m CROSS JOIN global g
),
ranked AS (
    SELECT user_id, score,
           row_number() OVER (ORDER BY score DESC, user_id ASC)::int AS rank,
           imports_n, rating_avg, rating_n, followers_n
      FROM scored
)
INSERT INTO author_ranks AS ar
    (user_id, score, rank, imports_30d, rating_avg_30d, rating_count_30d,
     followers_30d, window_days, computed_at)
SELECT user_id, score, rank, imports_n, rating_avg, rating_n, followers_n, %d, now()
  FROM ranked
ON CONFLICT (user_id) DO UPDATE SET
    prev_rank        = ar.rank,
    rank             = EXCLUDED.rank,
    score            = EXCLUDED.score,
    imports_30d      = EXCLUDED.imports_30d,
    rating_avg_30d   = EXCLUDED.rating_avg_30d,
    rating_count_30d = EXCLUDED.rating_count_30d,
    followers_30d    = EXCLUDED.followers_30d,
    computed_at      = now()`,
		WindowDays, minAccountAgeDays, minImporterAgeDays,
		minPublishedRecipes, minImports,
		weightImports,
		weightRating, bayesPrior, bayesPrior, bayesPrior,
		weightFollowers,
		WindowDays))
	if err != nil {
		return fmt.Errorf("ranking: refresh authors: %w", err)
	}
	return nil
}

// refreshShorts rewrites the ranked feed position for every published short.
//
// The score is frozen between refreshes precisely so a reader can page through
// it: keyset pagination over a live counter skips and repeats rows.
//
// Recency decays on a half-life rather than a cutoff, so a good short fades
// instead of falling off a cliff, and a new one is not buried by a month-old
// hit that accumulated views all month.
func (r *Repo) refreshShorts(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
WITH engagement AS (
    SELECT p.id,
           p.like_count + 2 * p.comment_count AS raw,
           extract(epoch FROM (now() - p.published_at)) / 86400.0 AS age_days
      FROM posts p
     WHERE p.kind = 'short' AND p.status = 'published' AND p.deleted_at IS NULL
),
maxima AS (SELECT greatest(max(raw), 1) AS max_raw FROM engagement)
UPDATE posts p
   SET feed_score = (
           0.7 * ln(1 + e.raw) / ln(1 + m.max_raw)
         + 0.3 * exp(-e.age_days / %f)
       )::numeric(12,6),
       feed_epoch = (SELECT epoch + 1 FROM ranking_state WHERE id)
  FROM engagement e CROSS JOIN maxima m
 WHERE p.id = e.id`, shortsHalfLifeDays)); err != nil {
		return fmt.Errorf("ranking: refresh shorts: %w", err)
	}
	return nil
}

// shortsHalfLifeDays sets how fast a short's recency term decays.
const shortsHalfLifeDays = 7.0

// Badge is the "top N" marker shown next to an avatar.
type Badge struct {
	Rank     int `json:"rank"`
	PrevRank int `json:"prev_rank,omitempty"`
}

// TopAuthors returns the leaderboard head.
//
// The badge itself is a handful of ids, so callers hold it in memory rather
// than joining author_ranks into the busiest query in the store.
func (r *Repo) TopAuthors(ctx context.Context, n int) (map[string]Badge, error) {
	if n <= 0 || n > 100 {
		n = BadgeTop
	}
	rows, err := r.pool.Query(ctx, `
		SELECT user_id::text, rank, coalesce(prev_rank, 0)
		  FROM author_ranks WHERE rank <= $1 ORDER BY rank ASC`, n)
	if err != nil {
		return nil, fmt.Errorf("ranking: top authors: %w", err)
	}
	defer rows.Close()

	out := map[string]Badge{}
	for rows.Next() {
		var id string
		var b Badge
		if err := rows.Scan(&id, &b.Rank, &b.PrevRank); err != nil {
			return nil, fmt.Errorf("ranking: scan top author: %w", err)
		}
		out[id] = b
	}
	return out, rows.Err()
}

// Entry is one row of the public leaderboard.
type Entry struct {
	Rank      int      `json:"rank"`
	PrevRank  int      `json:"prev_rank,omitempty"`
	UserID    string   `json:"user_id"`
	Handle    string   `json:"handle"`
	Name      string   `json:"name"`
	AvatarURL string   `json:"avatar_url,omitempty"`
	Verified  bool     `json:"verified,omitempty"`
	Imports   int      `json:"imports_30d"`
	Rating    *float64 `json:"rating,omitempty"`
	Followers int      `json:"followers_30d"`
}

// Leaderboard returns the public top list, resolving avatars through the same
// CDN resolver the rest of the store uses.
func (r *Repo) Leaderboard(ctx context.Context, limit int, media interface{ PublicURL(string) string }) ([]Entry, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ar.rank, coalesce(ar.prev_rank, 0), u.id::text, u.handle::text,
		       u.display_name, av.storage_key, u.verified_at IS NOT NULL,
		       ar.imports_30d, ar.rating_avg_30d, ar.followers_30d
		  FROM author_ranks ar
		  JOIN users u ON u.id = ar.user_id AND u.status = 'active' AND u.deleted_at IS NULL
		  LEFT JOIN media_assets av ON av.id = u.avatar_media_id AND av.status = 'ready'
		 ORDER BY ar.rank ASC, ar.user_id ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("ranking: leaderboard: %w", err)
	}
	defer rows.Close()

	out := []Entry{}
	for rows.Next() {
		var e Entry
		var avatarKey pgtype.Text
		var rating pgtype.Numeric
		if err := rows.Scan(&e.Rank, &e.PrevRank, &e.UserID, &e.Handle, &e.Name,
			&avatarKey, &e.Verified, &e.Imports, &rating, &e.Followers); err != nil {
			return nil, fmt.Errorf("ranking: scan entry: %w", err)
		}
		if avatarKey.Valid {
			e.AvatarURL = media.PublicURL(avatarKey.String)
		}
		if rating.Valid {
			if f, err := rating.Float64Value(); err == nil && f.Valid {
				v := f.Float64
				e.Rating = &v
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RankOf returns one author's badge, or nil when they are not ranked.
func (r *Repo) RankOf(ctx context.Context, userID string) (*Badge, error) {
	var b Badge
	err := r.pool.QueryRow(ctx, `
		SELECT rank, coalesce(prev_rank, 0) FROM author_ranks WHERE user_id = $1::uuid`,
		userID).Scan(&b.Rank, &b.PrevRank)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ranking: rank of: %w", err)
	}
	return &b, nil
}
