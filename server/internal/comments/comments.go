// Package comments serves discussion under recipes and posts.
//
// Star ratings stay a separate thing: one per person per recipe, feeding the
// score. A comment is a conversation, there may be many, and it carries no
// weight in ranking.
package comments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/paging"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
)

var (
	ErrNotFound = errors.New("comments: not found")
	// ErrTooFast throttles a burst from one account. Rate limiting by IP is not
	// enough here: a comment is attributable to a person, and spam arrives from
	// one account across many addresses more often than the reverse.
	ErrTooFast = errors.New("comments: too fast")
	// ErrBadParent covers replying to a reply, or to a comment on something
	// else. The database refuses both; this names which rule was hit.
	ErrBadParent = errors.New("comments: invalid parent")
)

// Limits.
const (
	MaxBody      = 2000
	DefaultLimit = 20
	MaxLimit     = 50
	// MaxPerMinute is one person's burst ceiling.
	MaxPerMinute = 10
	// MaxReplies caps how many replies are attached to each root in a page.
	// A thread with two hundred replies needs its own view, not a bigger page.
	MaxReplies = 3
)

// SortNew is the only ordering: a conversation reads newest-first at the root.
const SortNew = "new"

// Author is the byline, matching the shape used on cards everywhere else.
type Author struct {
	ID        string `json:"id"`
	Handle    string `json:"handle"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Verified  bool   `json:"verified,omitempty"`
}

// Comment is one message.
type Comment struct {
	ID         string    `json:"id"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
	Edited     bool      `json:"edited,omitempty"`
	Author     Author    `json:"author"`
	Replies    []Comment `json:"replies,omitempty"`
	ReplyCount int       `json:"reply_count,omitempty"`
	// Mine lets a client show edit and delete without a second lookup.
	Mine bool `json:"mine,omitempty"`
}

// Page is one keyset page of root comments, each with its first few replies.
type Page struct {
	Items      []Comment `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
	HasMore    bool      `json:"has_more"`
	Total      int64     `json:"total"`
}

// Target names what is being discussed.
type Target struct {
	Kind string // "recipe" | "post"
	ID   string
}

func (t Target) column() (string, error) {
	switch t.Kind {
	case "recipe":
		return "recipe_id", nil
	case "post":
		return "post_id", nil
	default:
		return "", fmt.Errorf("comments: unknown target kind %q", t.Kind)
	}
}

// Repo reads and writes comments.
type Repo struct {
	pool  *pgxpool.Pool
	media search.MediaURLResolver
}

func NewRepo(pool *pgxpool.Pool, media search.MediaURLResolver) *Repo {
	return &Repo{pool: pool, media: media}
}

// List returns a page of root comments with a few replies under each.
//
// Roots are keyset-paginated; replies are fetched for the whole page in one
// query. You cannot keyset-paginate rows in a nested structure, so only the
// roots carry a cursor.
func (r *Repo) List(ctx context.Context, t Target, viewerID string, cur *paging.Cursor, limit int) (Page, error) {
	col, err := t.column()
	if err != nil {
		return Page{}, err
	}
	if limit <= 0 || limit > MaxLimit {
		limit = DefaultLimit
	}

	args := []any{t.ID, limit + 1}
	// status = 'visible' verbatim: written as status <> 'removed' the planner
	// would stop using the partial root indexes entirely.
	where := fmt.Sprintf("c.%s = $1::uuid AND c.parent_id IS NULL AND c.status = 'visible'", col)
	if viewerID != "" {
		args = append(args, viewerID)
		where += fmt.Sprintf(` AND NOT EXISTS (
			SELECT 1 FROM user_blocks b
			 WHERE (b.blocker_id = $%d::uuid AND b.blocked_id = c.author_id)
			    OR (b.blocked_id = $%d::uuid AND b.blocker_id = c.author_id))`,
			len(args), len(args))
	}
	if cur != nil {
		args = append(args, cur.Time, cur.ID)
		where += fmt.Sprintf(" AND (c.created_at, c.id) < ($%d::timestamptz, $%d::uuid)",
			len(args)-1, len(args))
	}

	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		SELECT c.id::text, c.body, c.created_at, c.updated_at > c.created_at,
		       u.id::text, u.handle::text, u.display_name,
		       u.verified_at IS NOT NULL, av.storage_key,
		       (SELECT count(*) FROM comments rp
		         WHERE rp.parent_id = c.id AND rp.status = 'visible')::int
		  FROM comments c
		  JOIN users u ON u.id = c.author_id AND u.status = 'active'
		  LEFT JOIN media_assets av ON av.id = u.avatar_media_id AND av.status = 'ready'
		 WHERE %s
		 ORDER BY c.created_at DESC, c.id DESC
		 LIMIT $2`, where), args...)
	if err != nil {
		return Page{}, fmt.Errorf("comments: list roots: %w", err)
	}
	defer rows.Close()

	page := Page{Items: []Comment{}}
	for rows.Next() {
		c, err := scanComment(rows, r.media, viewerID)
		if err != nil {
			return Page{}, err
		}
		page.Items = append(page.Items, c)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("comments: iterate roots: %w", err)
	}

	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.HasMore = true
		last := page.Items[len(page.Items)-1]
		page.NextCursor = paging.Cursor{Sort: SortNew, ID: last.ID, Time: last.CreatedAt}.Encode()
	}
	if err := r.attachReplies(ctx, page.Items, viewerID); err != nil {
		return Page{}, err
	}

	if err := r.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT comment_count FROM %s WHERE id = $1::uuid`, targetTable(t.Kind)),
		t.ID).Scan(&page.Total); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Page{}, fmt.Errorf("comments: read total: %w", err)
	}
	return page, nil
}

func targetTable(kind string) string {
	if kind == "post" {
		return "posts"
	}
	return "recipes"
}

// attachReplies fetches replies for a whole page of roots in one query, rather
// than one query per root.
func (r *Repo) attachReplies(ctx context.Context, roots []Comment, viewerID string) error {
	if len(roots) == 0 {
		return nil
	}
	ids := make([]string, 0, len(roots))
	for _, c := range roots {
		if c.ReplyCount > 0 {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}

	args := []any{ids, MaxReplies}
	blocked := ""
	if viewerID != "" {
		args = append(args, viewerID)
		blocked = `AND NOT EXISTS (
			SELECT 1 FROM user_blocks b
			 WHERE (b.blocker_id = $3::uuid AND b.blocked_id = c.author_id)
			    OR (b.blocked_id = $3::uuid AND b.blocker_id = c.author_id))`
	}

	// LATERAL keeps this at MaxReplies rows per root instead of fetching whole
	// threads and trimming in Go.
	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		SELECT c.parent_id::text, c.id::text, c.body, c.created_at,
		       c.updated_at > c.created_at,
		       u.id::text, u.handle::text, u.display_name,
		       u.verified_at IS NOT NULL, av.storage_key, 0
		  FROM unnest($1::uuid[]) AS root(id)
		  JOIN LATERAL (
		      SELECT * FROM comments c2
		       WHERE c2.parent_id = root.id AND c2.status = 'visible'
		       ORDER BY c2.created_at ASC, c2.id ASC
		       LIMIT $2
		  ) c ON true
		  JOIN users u ON u.id = c.author_id AND u.status = 'active'
		  LEFT JOIN media_assets av ON av.id = u.avatar_media_id AND av.status = 'ready'
		 WHERE true %s
		 ORDER BY c.created_at ASC, c.id ASC`, blocked), args...)
	if err != nil {
		return fmt.Errorf("comments: list replies: %w", err)
	}
	defer rows.Close()

	byParent := map[string][]Comment{}
	for rows.Next() {
		var parent string
		var c Comment
		var avatarKey pgtype.Text
		if err := rows.Scan(&parent, &c.ID, &c.Body, &c.CreatedAt, &c.Edited,
			&c.Author.ID, &c.Author.Handle, &c.Author.Name,
			&c.Author.Verified, &avatarKey, &c.ReplyCount); err != nil {
			return fmt.Errorf("comments: scan reply: %w", err)
		}
		if avatarKey.Valid {
			c.Author.AvatarURL = r.media.PublicURL(avatarKey.String)
		}
		c.Mine = viewerID != "" && viewerID == c.Author.ID
		byParent[parent] = append(byParent[parent], c)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("comments: iterate replies: %w", err)
	}
	for i := range roots {
		roots[i].Replies = byParent[roots[i].ID]
	}
	return nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanComment(row rowScanner, media search.MediaURLResolver, viewerID string) (Comment, error) {
	var c Comment
	var avatarKey pgtype.Text
	if err := row.Scan(&c.ID, &c.Body, &c.CreatedAt, &c.Edited,
		&c.Author.ID, &c.Author.Handle, &c.Author.Name,
		&c.Author.Verified, &avatarKey, &c.ReplyCount); err != nil {
		return Comment{}, fmt.Errorf("comments: scan: %w", err)
	}
	if avatarKey.Valid {
		c.Author.AvatarURL = media.PublicURL(avatarKey.String)
	}
	c.Mine = viewerID != "" && viewerID == c.Author.ID
	return c, nil
}

// Create posts a comment or a reply.
func (r *Repo) Create(ctx context.Context, t Target, authorID, parentID, body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > MaxBody {
		return "", fmt.Errorf("comments: body must be 1..%d characters", MaxBody)
	}
	col, err := t.column()
	if err != nil {
		return "", err
	}

	// One person's burst ceiling, backed by comments_author_idx.
	var recent int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM comments
		 WHERE author_id = $1::uuid AND created_at > now() - interval '1 minute'`,
		authorID).Scan(&recent); err != nil {
		return "", fmt.Errorf("comments: rate check: %w", err)
	}
	if recent >= MaxPerMinute {
		return "", ErrTooFast
	}

	depth := 0
	if parentID != "" {
		depth = 1
	}
	var id string
	err = r.pool.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO comments (%s, author_id, parent_id, depth, body)
		SELECT $1::uuid, $2::uuid, nullif($3,'')::uuid, $4, $5
		 WHERE EXISTS (SELECT 1 FROM %s WHERE id = $1::uuid
		                AND status = 'published' AND deleted_at IS NULL)
		RETURNING id::text`, col, targetTable(t.Kind)),
		t.ID, authorID, parentID, int16(depth), body).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// The target is missing, unpublished or deleted. Commenting on it is
		// not something to explain in detail.
		return "", ErrNotFound
	}
	if err != nil {
		// The composite foreign keys reject a reply to a reply, and a reply
		// whose parent belongs to something else.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23503" || pgErr.Code == "23514") {
			return "", ErrBadParent
		}
		return "", fmt.Errorf("comments: create: %w", err)
	}
	return id, nil
}

// Delete removes a comment the caller wrote.
//
// It is a status change, not a row delete: a reply hanging off it would
// otherwise cascade away too, and the thread would lose messages nobody asked
// to remove.
func (r *Repo) Delete(ctx context.Context, commentID, authorID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE comments SET status = 'removed'
		 WHERE id = $1::uuid AND author_id = $2::uuid AND status <> 'removed'`,
		commentID, authorID)
	if err != nil {
		return fmt.Errorf("comments: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Hide is the moderator's version of Delete.
func (r *Repo) Hide(ctx context.Context, commentID, moderatorID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("comments: hide begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var authorID string
	err = tx.QueryRow(ctx, `
		UPDATE comments SET status = 'hidden'
		 WHERE id = $1::uuid AND status = 'visible'
		 RETURNING author_id::text`, commentID).Scan(&authorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("comments: hide: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE reports SET status = 'upheld', resolver_id = $2::uuid, resolved_at = now()
		 WHERE target_type = 'comment' AND target_id = $1::uuid
		   AND status IN ('open','reviewing')`, commentID, moderatorID); err != nil {
		return fmt.Errorf("comments: resolve reports: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO moderation_events (target_type, target_id, user_id, actor_id, action)
		VALUES ('comment', $1::uuid, $2::uuid, $3::uuid, 'takedown')`,
		commentID, authorID, moderatorID); err != nil {
		return fmt.Errorf("comments: log hide: %w", err)
	}
	return tx.Commit(ctx)
}
