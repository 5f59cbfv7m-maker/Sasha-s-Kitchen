// Package author serves public author pages: the profile header, the author's
// own listings, and the follow relationship behind them.
//
// The recipe listing itself is not implemented here. internal/search already
// pages, filters, facets and block-filters recipes, and it gained an AuthorID
// filter for exactly this: an author's shelf is the storefront engine with one
// field set, not a second query path that could drift from it.
package author

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
)

var (
	// ErrNotFound covers "no such author", "suspended", "deleted" and "blocked
	// in either direction" on purpose. Distinguishing them would tell a blocked
	// user that the person who blocked them exists and is worth another try
	// from a second account.
	ErrNotFound = errors.New("author: not found")
	// ErrSelf is returned when someone tries to follow themselves.
	ErrSelf = errors.New("author: cannot follow yourself")
)

// Link is one outbound link from a profile.
type Link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Stats are the counters under a profile header. They are read from user_stats
// rather than counted at read time; a COUNT(*) per profile view is what makes a
// storefront slow once anyone is popular.
type Stats struct {
	Recipes     int      `json:"recipes"`
	Posts       int      `json:"posts"`
	Shorts      int      `json:"shorts"`
	Followers   int      `json:"followers"`
	Following   int      `json:"following"`
	Imports     int64    `json:"imports"`
	Rating      *float64 `json:"rating,omitempty"`
	RatingCount int      `json:"rating_count"`
}

// ViewerState is what the signed-in reader may do with this page. It is the
// only viewer-dependent part of the response.
type ViewerState struct {
	IsSelf    bool `json:"is_self"`
	Following bool `json:"following"`
}

// Cover is the hero media at the top of the page: a still, or a muted looping
// video with a poster, mirroring how a card carries media.
type Cover struct {
	Kind      string `json:"kind"`
	URL       string `json:"url,omitempty"`
	PosterURL string `json:"poster_url,omitempty"`
	Blurhash  string `json:"blurhash,omitempty"`
}

// Profile is the author page header.
type Profile struct {
	ID          string       `json:"id"`
	Handle      string       `json:"handle"`
	DisplayName string       `json:"display_name"`
	Bio         string       `json:"bio,omitempty"`
	Location    string       `json:"location,omitempty"`
	AvatarURL   string       `json:"avatar_url,omitempty"`
	Cover       *Cover       `json:"cover,omitempty"`
	Links       []Link       `json:"links"`
	Verified    bool         `json:"verified"`
	JoinedAt    time.Time    `json:"joined_at"`
	Stats       Stats        `json:"stats"`
	Featured    *search.Card `json:"featured,omitempty"`
	// Rank is the "top N" badge beside the avatar, absent when the author is
	// not on the leaderboard.
	Rank   *RankBadge  `json:"rank,omitempty"`
	Viewer ViewerState `json:"viewer"`
}

// RankBadge marks an author's leaderboard position and how it moved.
type RankBadge struct {
	Rank     int `json:"rank"`
	PrevRank int `json:"prev_rank,omitempty"`
}

// Repo reads author pages.
type Repo struct {
	pool   *pgxpool.Pool
	search *search.Repo
	media  search.MediaURLResolver
}

func NewRepo(pool *pgxpool.Pool, s *search.Repo, media search.MediaURLResolver) *Repo {
	return &Repo{pool: pool, search: s, media: media}
}

// Resolve turns the path segment of an author URL into a user id.
//
// A handle can change, and links that were already shared must keep working,
// so a miss falls back to user_handle_history. The order matters and cannot be
// reversed: deleting an account frees its handle for reuse (0001 does this
// deliberately), so a stale history row must never outrank whoever holds the
// handle now.
func (r *Repo) Resolve(ctx context.Context, handleOrID string) (string, error) {
	if id, err := uuid.Parse(handleOrID); err == nil {
		var out string
		err := r.pool.QueryRow(ctx, `
			SELECT id::text FROM users
			 WHERE id = $1::uuid AND deleted_at IS NULL AND status = 'active'`,
			id.String()).Scan(&out)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		if err != nil {
			return "", fmt.Errorf("author: resolve id: %w", err)
		}
		return out, nil
	}

	handle := strings.ToLower(strings.TrimSpace(handleOrID))
	var out string
	err := r.pool.QueryRow(ctx, `
		SELECT id::text FROM users
		 WHERE handle = $1 AND deleted_at IS NULL AND status = 'active'`, handle).Scan(&out)
	if err == nil {
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("author: resolve handle: %w", err)
	}

	err = r.pool.QueryRow(ctx, `
		SELECT u.id::text
		  FROM user_handle_history h
		  JOIN users u ON u.id = h.user_id
		 WHERE h.old_handle = $1 AND u.deleted_at IS NULL AND u.status = 'active'`,
		handle).Scan(&out)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("author: resolve former handle: %w", err)
	}
	return out, nil
}

// blocked reports whether a block exists in EITHER direction.
//
// Both directions matter. Hiding the blocker from the blocked user is the
// point of blocking; hiding the blocked user from the blocker is what stops a
// block from being a one-way mirror the blocker has to keep looking through.
func (r *Repo) blocked(ctx context.Context, a, b string) (bool, error) {
	if a == "" || b == "" || a == b {
		return false, nil
	}
	var yes bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_blocks
			 WHERE (blocker_id = $1::uuid AND blocked_id = $2::uuid)
			    OR (blocker_id = $2::uuid AND blocked_id = $1::uuid))`,
		a, b).Scan(&yes); err != nil {
		return false, fmt.Errorf("author: check block: %w", err)
	}
	return yes, nil
}

// Profile renders the page header for authorID as seen by viewerID ("" when
// anonymous).
func (r *Repo) Profile(ctx context.Context, authorID, viewerID string) (*Profile, error) {
	hidden, err := r.blocked(ctx, authorID, viewerID)
	if err != nil {
		return nil, err
	}
	if hidden {
		return nil, ErrNotFound
	}

	var (
		p           Profile
		bio         pgtype.Text
		location    pgtype.Text
		avatarKey   pgtype.Text
		coverKind   pgtype.Text
		coverKey    pgtype.Text
		coverPoster pgtype.Text
		coverHash   pgtype.Text
		rating      pgtype.Numeric
		linksRaw    []byte
		featuredID  pgtype.UUID
		trustLevel  int16
	)

	err = r.pool.QueryRow(ctx, `
		SELECT u.id::text, u.handle::text, u.display_name, u.bio, u.location,
		       av.storage_key,
		       cv.kind, cv.storage_key, cv.poster_key, cv.blurhash,
		       u.links, u.verified_at IS NOT NULL, u.trust_level, u.created_at,
		       u.featured_recipe_id,
		       s.recipe_count, s.post_count, s.short_count,
		       s.follower_count, s.following_count, s.import_total,
		       s.rating_avg, s.rating_count,
		       EXISTS (SELECT 1 FROM follows f
		                WHERE f.follower_id = nullif($2,'')::uuid AND f.author_id = u.id)
		  FROM users u
		  JOIN user_stats s ON s.user_id = u.id
		  LEFT JOIN media_assets av ON av.id = u.avatar_media_id AND av.status = 'ready'
		  LEFT JOIN media_assets cv ON cv.id = u.cover_media_id  AND cv.status = 'ready'
		 WHERE u.id = $1::uuid AND u.deleted_at IS NULL AND u.status = 'active'`,
		authorID, viewerID).Scan(
		&p.ID, &p.Handle, &p.DisplayName, &bio, &location,
		&avatarKey,
		&coverKind, &coverKey, &coverPoster, &coverHash,
		&linksRaw, &p.Verified, &trustLevel, &p.JoinedAt,
		&featuredID,
		&p.Stats.Recipes, &p.Stats.Posts, &p.Stats.Shorts,
		&p.Stats.Followers, &p.Stats.Following, &p.Stats.Imports,
		&rating, &p.Stats.RatingCount,
		&p.Viewer.Following,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("author: load profile: %w", err)
	}

	p.Bio = bio.String
	p.Location = location.String
	p.Viewer.IsSelf = viewerID != "" && viewerID == p.ID
	if avatarKey.Valid {
		p.AvatarURL = r.media.PublicURL(avatarKey.String)
	}
	if coverKind.Valid {
		c := &Cover{Kind: coverKind.String, Blurhash: coverHash.String}
		// Video plays from the HLS manifest with the poster as its still frame;
		// a photo is served directly. Originals are never exposed, same rule as
		// the card media in internal/search.
		if c.Kind == "video" {
			c.PosterURL = r.media.PublicURL(coverPoster.String)
		} else if coverKey.Valid {
			c.URL = r.media.PublicURL(coverKey.String)
		}
		p.Cover = c
	}
	if rating.Valid {
		if f, err := rating.Float64Value(); err == nil && f.Valid {
			v := f.Float64
			p.Stats.Rating = &v
		}
	}

	// Links are only shown for authors who have earned some trust. An
	// unmoderated public page with arbitrary outbound links is a spam vector,
	// and the quota table decides who has earned it.
	p.Links = []Link{}
	var mayShowLinks bool
	if err := r.pool.QueryRow(ctx,
		`SELECT may_show_links FROM trust_levels WHERE level = $1`, trustLevel).Scan(&mayShowLinks); err != nil &&
		!errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("author: read trust level: %w", err)
	}
	if mayShowLinks && len(linksRaw) > 0 {
		p.Links = decodeLinks(linksRaw)
	}

	// The leaderboard is a separate small table; one indexed lookup keeps it
	// out of the storefront's hot card query.
	var rank, prevRank int
	switch err := r.pool.QueryRow(ctx, `
		SELECT rank, coalesce(prev_rank, 0) FROM author_ranks WHERE user_id = $1::uuid`,
		authorID).Scan(&rank, &prevRank); {
	case err == nil:
		p.Rank = &RankBadge{Rank: rank, PrevRank: prevRank}
	case errors.Is(err, pgx.ErrNoRows):
		// Not ranked; the badge is simply absent.
	default:
		return nil, fmt.Errorf("author: read rank: %w", err)
	}

	if featuredID.Valid {
		// ListOne re-applies published/active/block rules, so a featured recipe
		// that was taken down, unpublished or belongs to a blocked author
		// simply vanishes from the header instead of 404-ing the page.
		card, err := r.search.ListOne(ctx, search.Query{}, uuidString(featuredID), viewerID)
		if err == nil && card != nil && card.Author.ID == p.ID {
			p.Featured = card
		}
	}
	return &p, nil
}

// Follow records a subscription. It is idempotent: the client cannot know
// whether its first tap landed.
func (r *Repo) Follow(ctx context.Context, followerID, authorID string) error {
	if followerID == authorID {
		return ErrSelf
	}
	hidden, err := r.blocked(ctx, followerID, authorID)
	if err != nil {
		return err
	}
	if hidden {
		// A block in either direction hides the author, and following someone
		// you cannot see is incoherent. 404 keeps the block undetectable.
		return ErrNotFound
	}

	tag, err := r.pool.Exec(ctx, `
		INSERT INTO follows (follower_id, author_id)
		SELECT $1::uuid, u.id FROM users u
		 WHERE u.id = $2::uuid AND u.deleted_at IS NULL AND u.status = 'active'
		ON CONFLICT DO NOTHING`, followerID, authorID)
	if err != nil {
		return fmt.Errorf("author: follow: %w", err)
	}
	// No row inserted means either "already following" or "no such author".
	// Distinguish them, so following a ghost is not reported as success.
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := r.pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM follows
			                WHERE follower_id = $1::uuid AND author_id = $2::uuid)`,
			followerID, authorID).Scan(&exists); err != nil {
			return fmt.Errorf("author: confirm follow: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

// Unfollow removes a subscription. Also idempotent.
func (r *Repo) Unfollow(ctx context.Context, followerID, authorID string) error {
	if _, err := r.pool.Exec(ctx, `
		DELETE FROM follows WHERE follower_id = $1::uuid AND author_id = $2::uuid`,
		followerID, authorID); err != nil {
		return fmt.Errorf("author: unfollow: %w", err)
	}
	return nil
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}
