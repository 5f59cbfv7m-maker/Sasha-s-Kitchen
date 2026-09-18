package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/posts"
)

// PostDraft is what an author submits when writing or editing.
type PostDraft struct {
	Kind         string        `json:"kind"`
	Title        string        `json:"title"`
	Excerpt      string        `json:"excerpt,omitempty"`
	Slug         string        `json:"slug,omitempty"`
	Blocks       []posts.Block `json:"blocks"`
	CoverMediaID string        `json:"cover_media_id,omitempty"`
	VideoMediaID string        `json:"video_media_id,omitempty"`
}

// OwnPost is one row of the author's own list, including states the public
// listings deliberately hide.
type OwnPost struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug,omitempty"`
	Status      string     `json:"status"`
	Rejected    string     `json:"rejected_reason,omitempty"`
	VideoReady  bool       `json:"video_ready"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ListOwnPosts returns everything the author has, in every state. It is backed
// by posts_author_status_idx, which exists precisely because every public
// listing index excludes drafts.
func (r *Repo) ListOwnPosts(ctx context.Context, authorID string) ([]OwnPost, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, kind, title, coalesce(slug,''), status,
		       coalesce(rejected_reason,''), video_ready, published_at, updated_at
		  FROM posts
		 WHERE author_id = $1::uuid AND deleted_at IS NULL
		 ORDER BY updated_at DESC, id DESC`, authorID)
	if err != nil {
		return nil, fmt.Errorf("studio: list own posts: %w", err)
	}
	defer rows.Close()

	out := []OwnPost{}
	for rows.Next() {
		var p OwnPost
		var published pgtype.Timestamptz
		if err := rows.Scan(&p.ID, &p.Kind, &p.Title, &p.Slug, &p.Status,
			&p.Rejected, &p.VideoReady, &published, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("studio: scan own post: %w", err)
		}
		if published.Valid {
			t := published.Time
			p.PublishedAt = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ValidateDraft checks a draft's shape and its references, returning per-field
// Russian messages.
func (r *Repo) ValidateDraft(ctx context.Context, authorID string, d *PostDraft) (map[string]string, error) {
	problems := map[string]string{}

	switch d.Kind {
	case posts.KindArticle, posts.KindShort:
	default:
		problems["kind"] = "Допустимые значения: article, short"
	}

	d.Title = strings.TrimSpace(d.Title)
	if n := len([]rune(d.Title)); n == 0 || n > 140 {
		problems["title"] = "Заголовок от 1 до 140 символов"
	}
	d.Excerpt = strings.TrimSpace(d.Excerpt)
	if len([]rune(d.Excerpt)) > 400 {
		problems["excerpt"] = "Не более 400 символов"
	}

	if d.Kind == posts.KindArticle {
		// A slug is part of a shareable URL, so it is derived from the title
		// when the author does not supply one.
		if d.Slug == "" {
			d.Slug = slugify(d.Title)
		} else {
			d.Slug = slugify(d.Slug)
		}
		if d.Slug == "" {
			problems["slug"] = "Не удалось составить адрес: добавьте латиницу или цифры в заголовок или задайте адрес вручную"
		}
	} else {
		// A short is addressed by id; a slug would be noise.
		d.Slug = ""
		if d.VideoMediaID == "" {
			problems["video_media_id"] = "Короткое видео обязано содержать видео"
		}
	}

	if shape, normalized := posts.ValidateBlocks(d.Blocks); shape != nil {
		for k, v := range shape {
			problems[k] = v
		}
	} else {
		d.Blocks = normalized
	}

	// Cover and video are references like any block reference, so they go
	// through the same ownership check.
	refs := append([]posts.Block{}, d.Blocks...)
	if d.CoverMediaID != "" {
		refs = append(refs, posts.Block{Type: posts.BlockPhoto, MediaID: d.CoverMediaID})
	}
	if d.VideoMediaID != "" {
		refs = append(refs, posts.Block{Type: posts.BlockVideo, MediaID: d.VideoMediaID})
	}
	if len(problems) == 0 {
		refProblems, err := r.posts.ValidateRefs(ctx, authorID, refs)
		if err != nil {
			return nil, err
		}
		for k, v := range refProblems {
			problems[k] = v
		}
	}

	if len(problems) > 0 {
		return problems, nil
	}
	return nil, nil
}

// CreatePost stores a new draft. Nothing is published here: publishing is
// always a separate, explicit step that goes through the quota gate.
func (r *Repo) CreatePost(ctx context.Context, authorID string, d PostDraft) (string, error) {
	raw, err := json.Marshal(d.Blocks)
	if err != nil {
		return "", fmt.Errorf("studio: encode blocks: %w", err)
	}
	var id string
	err = r.pool.QueryRow(ctx, `
		INSERT INTO posts (author_id, kind, title, excerpt, slug, body_blocks,
		                   cover_media_id, video_media_id, status)
		VALUES ($1::uuid, $2, $3, nullif($4,''), nullif($5,''), $6::jsonb,
		        nullif($7,'')::uuid, nullif($8,'')::uuid, 'draft')
		RETURNING id::text`,
		authorID, d.Kind, d.Title, d.Excerpt, d.Slug, string(raw),
		d.CoverMediaID, d.VideoMediaID).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return "", ErrDuplicateSlug
		}
		return "", fmt.Errorf("studio: create post: %w", err)
	}
	// video_ready is derived from the asset, and the trigger on media_assets
	// only fires on a status change. A video that was already ready when the
	// post was written needs the flag set now.
	if err := r.refreshVideoReady(ctx, id); err != nil {
		return "", err
	}
	return id, nil
}

// ErrDuplicateSlug is returned when an author already has a post at that
// address. Slugs are unique per author, not globally.
var ErrDuplicateSlug = errors.New("studio: slug already used")

// UpdatePost edits a draft or a published post.
//
// Editing a published post sends it back through review when the author is
// premoderated: otherwise "publish something harmless, then rewrite it" is an
// open door straight past moderation.
func (r *Repo) UpdatePost(ctx context.Context, authorID, postID string, d PostDraft) error {
	raw, err := json.Marshal(d.Blocks)
	if err != nil {
		return fmt.Errorf("studio: encode blocks: %w", err)
	}
	policy, err := r.Policy(ctx, authorID)
	if err != nil {
		return err
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE posts SET
		    title = $3, excerpt = nullif($4,''), slug = nullif($5,''),
		    body_blocks = $6::jsonb,
		    cover_media_id = nullif($7,'')::uuid,
		    video_media_id = nullif($8,'')::uuid,
		    status = CASE
		        WHEN status = 'published' AND $9 THEN 'review'
		        WHEN status = 'rejected' THEN 'draft'
		        ELSE status END,
		    published_at = CASE
		        WHEN status = 'published' AND $9 THEN NULL
		        ELSE published_at END,
		    rejected_reason = NULL
		 WHERE id = $1::uuid AND author_id = $2::uuid AND deleted_at IS NULL`,
		postID, authorID, d.Title, d.Excerpt, d.Slug, string(raw),
		d.CoverMediaID, d.VideoMediaID, policy.Premoderate)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicateSlug
		}
		return fmt.Errorf("studio: update post: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return r.refreshVideoReady(ctx, postID)
}

// DeletePost soft-deletes, so moderation history and any report about it keep
// pointing at something real.
func (r *Repo) DeletePost(ctx context.Context, authorID, postID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE posts SET deleted_at = now(), status = 'archived'
		 WHERE id = $1::uuid AND author_id = $2::uuid AND deleted_at IS NULL`,
		postID, authorID)
	if err != nil {
		return fmt.Errorf("studio: delete post: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SubmitPost is the publish step.
//
// A trusted author's post goes live immediately; a new author's goes into the
// review queue. Either way it counts against the daily quota, because the quota
// exists to bound how much any one account can push at the store in a day.
func (r *Repo) SubmitPost(ctx context.Context, authorID, postID string) (*publishOutcome, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("studio: submit begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	policy, err := r.publishGate(ctx, tx, authorID)
	if err != nil {
		return nil, err
	}

	// A short with no playable video would publish as a dead card.
	var kind string
	var videoReady bool
	var status string
	err = tx.QueryRow(ctx, `
		SELECT kind, video_ready, status FROM posts
		 WHERE id = $1::uuid AND author_id = $2::uuid AND deleted_at IS NULL`,
		postID, authorID).Scan(&kind, &videoReady, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("studio: read post: %w", err)
	}
	if status != "draft" && status != "rejected" {
		return nil, ErrBadState
	}

	next := "review"
	if !policy.Premoderate {
		next = "published"
	}

	var published pgtype.Timestamptz
	err = tx.QueryRow(ctx, `
		UPDATE posts
		   SET status = $3,
		       published_at = CASE WHEN $3 = 'published'
		                           THEN coalesce(published_at, now()) ELSE published_at END,
		       rejected_reason = NULL
		 WHERE id = $1::uuid AND author_id = $2::uuid AND deleted_at IS NULL
		 RETURNING published_at`, postID, authorID, next).Scan(&published)
	if err != nil {
		return nil, fmt.Errorf("studio: submit post: %w", err)
	}

	if err := r.mod.LogEvent(ctx, "post", postID, authorID, authorID, "submitted", ""); err != nil {
		return nil, err
	}
	if next == "published" {
		if _, err := tx.Exec(ctx, `
			UPDATE user_stats SET post_count = post_count + 1
			 WHERE user_id = $1::uuid`, authorID); err != nil {
			return nil, fmt.Errorf("studio: bump post count: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("studio: submit commit: %w", err)
	}

	out := &publishOutcome{Status: next}
	if published.Valid {
		t := published.Time
		out.PublishedAt = &t
	}
	return out, nil
}

// refreshVideoReady syncs the derived flag with the asset's current status.
func (r *Repo) refreshVideoReady(ctx context.Context, postID string) error {
	if _, err := r.pool.Exec(ctx, `
		UPDATE posts p SET video_ready = coalesce(
		    (SELECT m.status = 'ready' FROM media_assets m WHERE m.id = p.video_media_id), false)
		 WHERE p.id = $1::uuid`, postID); err != nil {
		return fmt.Errorf("studio: refresh video_ready: %w", err)
	}
	return nil
}

// slugify reduces a title to the slug alphabet, transliterating Cyrillic so a
// Russian title still yields a readable address rather than an empty one.
func slugify(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if repl, ok := translit[r]; ok {
			b.WriteString(repl)
			lastDash = false
			continue
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case unicode.IsSpace(r), r == '-', r == '_':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 80 {
		out = strings.Trim(out[:80], "-")
	}
	return out
}

var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch",
	'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}
