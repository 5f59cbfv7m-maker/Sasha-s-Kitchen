// Package posts serves the blog and the short-video feed.
//
// Articles and shorts are one table discriminated by kind. A short is a post
// whose body is a single video, so keeping them together means one moderation
// queue, one report path, one block filter, one draft workflow and one quota
// instead of two of each.
package posts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/paging"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
)

var ErrNotFound = errors.New("posts: not found")

// Kinds, mirroring the posts.kind CHECK constraint.
const (
	KindArticle = "article"
	KindShort   = "short"
)

// Sort orderings. Both keyset columns always go the same way, so the row-wise
// cursor comparison matches the index.
const (
	SortNew = "new" // published_at DESC, id DESC
	SortTop = "top" // feed_score  DESC, id DESC
)

// DefaultLimit and MaxLimit mirror internal/search so a client does not have
// to remember two sets of paging rules.
const (
	DefaultLimit = 20
	MaxLimit     = 50
)

// Author is the byline on a post card, matching the recipe card's shape so a
// client renders one author chip everywhere.
type Author struct {
	ID        string `json:"id"`
	Handle    string `json:"handle"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Verified  bool   `json:"verified,omitempty"`
}

// Card is one post in a listing: enough to render a tile, never the body.
type Card struct {
	ID           string        `json:"id"`
	Kind         string        `json:"kind"`
	Slug         string        `json:"slug,omitempty"`
	Title        string        `json:"title"`
	Excerpt      string        `json:"excerpt,omitempty"`
	PublishedAt  time.Time     `json:"published_at"`
	LikeCount    int64         `json:"like_count"`
	CommentCount int64         `json:"comment_count"`
	Author       Author        `json:"author"`
	Media        *search.Media `json:"media,omitempty"`
}

// Page is one keyset page of post cards.
type Page struct {
	Items      []Card `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// RenderedBlock is a body block with its references resolved. The stored form
// carries ids; a client receives URLs and recipe cards, never storage keys.
type RenderedBlock struct {
	Type    string   `json:"type"`
	Text    string   `json:"text,omitempty"`
	Level   int      `json:"level,omitempty"`
	Ordered bool     `json:"ordered,omitempty"`
	Items   []string `json:"items,omitempty"`
	Caption string   `json:"caption,omitempty"`

	Media  []search.Media `json:"media,omitempty"`
	Recipe *search.Card   `json:"recipe,omitempty"`
}

// Detail is a whole post.
type Detail struct {
	Card
	Blocks []RenderedBlock `json:"blocks"`
}

// Repo reads posts.
type Repo struct {
	pool   *pgxpool.Pool
	search *search.Repo
	media  search.MediaURLResolver
}

func NewRepo(pool *pgxpool.Pool, s *search.Repo, media search.MediaURLResolver) *Repo {
	return &Repo{pool: pool, search: s, media: media}
}

// cardColumns is the listing projection. As in internal/search, the scan order
// below must match it exactly.
const cardColumns = `
        p.id::text, p.kind, coalesce(p.slug,''), p.title, coalesce(p.excerpt,''),
        p.published_at, p.like_count, p.comment_count,
        u.id::text, u.handle::text, u.display_name,
        u.verified_at IS NOT NULL, av.storage_key,
        cm.kind, cm.storage_key, cm.poster_key, cm.hls_key, cm.blurhash, cm.width, cm.height`

const cardJoins = `
      JOIN users u ON u.id = p.author_id
      LEFT JOIN media_assets av ON av.id = u.avatar_media_id AND av.status = 'ready'
      LEFT JOIN media_assets cm ON cm.id = coalesce(p.cover_media_id, p.video_media_id)
                               AND cm.status = 'ready'`

// ListOptions selects a slice of posts.
type ListOptions struct {
	AuthorID string // "" lists across all authors
	Kind     string
	Sort     string
	Cursor   *paging.Cursor
	Limit    int
	// RequireVideoReady keeps a short out of the feed until its HLS exists,
	// which is what stops a dead card from being shown.
	RequireVideoReady bool
}

// List returns one keyset page.
func (r *Repo) List(ctx context.Context, o ListOptions, viewerID string) (Page, error) {
	if o.Limit <= 0 || o.Limit > MaxLimit {
		o.Limit = DefaultLimit
	}
	a := &argset{}

	// These match the partial indexes exactly; changing one means changing the
	// other, or the planner silently stops using them.
	where := []string{"p.status = 'published'", "p.deleted_at IS NULL", "u.status = 'active'"}
	where = append(where, fmt.Sprintf("p.kind = %s", a.next(o.Kind)))
	if o.RequireVideoReady {
		where = append(where, "p.video_ready")
	}
	if o.AuthorID != "" {
		where = append(where, fmt.Sprintf("p.author_id = %s::uuid", a.next(o.AuthorID)))
	}
	if viewerID != "" {
		// Same rule the storefront applies to recipes: a blocked author's work
		// disappears for that viewer, in either direction.
		where = append(where, fmt.Sprintf(`NOT EXISTS (
			SELECT 1 FROM user_blocks b
			 WHERE (b.blocker_id = %s::uuid AND b.blocked_id = p.author_id)
			    OR (b.blocked_id = %s::uuid AND b.blocker_id = p.author_id))`,
			a.next(viewerID), a.next(viewerID)))
	}

	order := "p.published_at DESC, p.id DESC"
	if o.Sort == SortTop {
		order = "p.feed_score DESC, p.id DESC"
	}
	if o.Cursor != nil {
		if o.Sort == SortTop {
			where = append(where, fmt.Sprintf("(p.feed_score, p.id) < (%s::numeric, %s::uuid)",
				a.next(o.Cursor.Num), a.next(o.Cursor.ID)))
		} else {
			where = append(where, fmt.Sprintf("(p.published_at, p.id) < (%s::timestamptz, %s::uuid)",
				a.next(o.Cursor.Time), a.next(o.Cursor.ID)))
		}
	}

	// One extra row tells us whether another page exists without a second
	// COUNT over the same predicate.
	sql := fmt.Sprintf(`
    SELECT %s, p.feed_score
      FROM posts p%s
     WHERE %s
     ORDER BY %s
     LIMIT %s`, cardColumns, cardJoins, joinAnd(where), order, a.next(o.Limit+1))

	rows, err := r.pool.Query(ctx, sql, a.vals...)
	if err != nil {
		return Page{}, fmt.Errorf("posts: list: %w", err)
	}
	defer rows.Close()

	page := Page{Items: []Card{}}
	scores := []float64{}
	for rows.Next() {
		c, score, err := scanCard(rows, r.media)
		if err != nil {
			return Page{}, err
		}
		page.Items = append(page.Items, c)
		scores = append(scores, score)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("posts: list iterate: %w", err)
	}

	if len(page.Items) > o.Limit {
		page.Items = page.Items[:o.Limit]
		scores = scores[:o.Limit]
		page.HasMore = true
		last := page.Items[len(page.Items)-1]
		c := paging.Cursor{Sort: o.Sort, ID: last.ID}
		if o.Sort == SortTop {
			c.Num = scores[len(scores)-1]
		} else {
			c.Time = last.PublishedAt
		}
		page.NextCursor = c.Encode()
	}
	return page, nil
}

// Get returns one post with its body rendered.
//
// idOrSlug accepts a uuid, or "<author handle>/<slug>" for an article, because
// article slugs are unique per author rather than globally.
func (r *Repo) Get(ctx context.Context, postID, viewerID string) (*Detail, error) {
	a := &argset{}
	where := []string{
		"p.status = 'published'", "p.deleted_at IS NULL", "u.status = 'active'",
		fmt.Sprintf("p.id = %s::uuid", a.next(postID)),
	}
	if viewerID != "" {
		where = append(where, fmt.Sprintf(`NOT EXISTS (
			SELECT 1 FROM user_blocks b
			 WHERE (b.blocker_id = %s::uuid AND b.blocked_id = p.author_id)
			    OR (b.blocked_id = %s::uuid AND b.blocker_id = p.author_id))`,
			a.next(viewerID), a.next(viewerID)))
	}

	sql := fmt.Sprintf(`
    SELECT %s, p.feed_score, p.body_blocks
      FROM posts p%s
     WHERE %s
     LIMIT 1`, cardColumns, cardJoins, joinAnd(where))

	var raw []byte
	row := r.pool.QueryRow(ctx, sql, a.vals...)
	c, _, err := scanCardWithBody(row, r.media, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	blocks, err := DecodeBlocks(raw)
	if err != nil {
		return nil, err
	}
	rendered, err := r.renderBlocks(ctx, blocks, viewerID)
	if err != nil {
		return nil, err
	}
	return &Detail{Card: c, Blocks: rendered}, nil
}

// ResolveArticle turns an author id and slug into a post id.
func (r *Repo) ResolveArticle(ctx context.Context, authorID, slug string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `
		SELECT id::text FROM posts
		 WHERE author_id = $1::uuid AND slug = $2 AND deleted_at IS NULL`,
		authorID, slug).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("posts: resolve article: %w", err)
	}
	return id, nil
}

func joinAnd(where []string) string {
	out := ""
	for i, w := range where {
		if i > 0 {
			out += "\n       AND "
		}
		out += w
	}
	return out
}

// argset accumulates bind parameters, as in internal/search: no caller-supplied
// value is ever concatenated into SQL.
type argset struct{ vals []any }

func (a *argset) next(v any) string {
	a.vals = append(a.vals, v)
	return "$" + itoa(len(a.vals))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

type rowScanner interface{ Scan(dest ...any) error }

func scanCard(row rowScanner, media search.MediaURLResolver) (Card, float64, error) {
	return scanCardWithBody(row, media, nil)
}

func scanCardWithBody(row rowScanner, media search.MediaURLResolver, body *[]byte) (Card, float64, error) {
	var (
		c          Card
		excerpt    string
		avatarKey  pgtype.Text
		mediaKind  pgtype.Text
		storageKey pgtype.Text
		posterKey  pgtype.Text
		hlsKey     pgtype.Text
		blurhash   pgtype.Text
		width      pgtype.Int4
		height     pgtype.Int4
		score      pgtype.Numeric
	)

	dest := []any{
		&c.ID, &c.Kind, &c.Slug, &c.Title, &excerpt,
		&c.PublishedAt, &c.LikeCount, &c.CommentCount,
		&c.Author.ID, &c.Author.Handle, &c.Author.Name,
		&c.Author.Verified, &avatarKey,
		&mediaKind, &storageKey, &posterKey, &hlsKey, &blurhash, &width, &height,
		&score,
	}
	if body != nil {
		dest = append(dest, body)
	}
	if err := row.Scan(dest...); err != nil {
		return Card{}, 0, err
	}

	c.Excerpt = excerpt
	if avatarKey.Valid {
		c.Author.AvatarURL = media.PublicURL(avatarKey.String)
	}
	if mediaKind.Valid {
		c.Media = resolveMedia(media, mediaKind.String, storageKey, posterKey, hlsKey, blurhash, width, height)
	}
	var scoreF float64
	if score.Valid {
		if f, err := score.Float64Value(); err == nil && f.Valid {
			scoreF = f.Float64
		}
	}
	return c, scoreF, nil
}

// resolveMedia applies the same rule as internal/search: video plays from the
// HLS manifest with the poster as its still frame, a photo is served directly,
// and originals are never exposed.
func resolveMedia(res search.MediaURLResolver, kind string,
	storageKey, posterKey, hlsKey, blurhash pgtype.Text, width, height pgtype.Int4) *search.Media {
	m := &search.Media{Kind: kind, Blurhash: blurhash.String}
	if kind == "video" {
		if hlsKey.Valid {
			m.URL = res.PublicURL(hlsKey.String)
		}
		if posterKey.Valid {
			m.PosterURL = res.PublicURL(posterKey.String)
		}
	} else if storageKey.Valid {
		m.URL = res.PublicURL(storageKey.String)
	}
	m.Width = int(width.Int32)
	m.Height = int(height.Int32)
	return m
}
