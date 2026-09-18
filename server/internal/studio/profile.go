package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/author"
)

// ErrHandleTaken is returned when a requested handle belongs to someone else.
var ErrHandleTaken = errors.New("studio: handle already taken")

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ProfileEdit is a partial update: a nil field is left alone, which is what
// lets a client PATCH one thing without having to resend the whole profile.
type ProfileEdit struct {
	Handle           *string        `json:"handle,omitempty"`
	DisplayName      *string        `json:"display_name,omitempty"`
	Bio              *string        `json:"bio,omitempty"`
	Location         *string        `json:"location,omitempty"`
	Links            *[]author.Link `json:"links,omitempty"`
	AvatarMediaID    *string        `json:"avatar_media_id,omitempty"`
	CoverMediaID     *string        `json:"cover_media_id,omitempty"`
	FeaturedRecipeID *string        `json:"featured_recipe_id,omitempty"`
	FeaturedPostID   *string        `json:"featured_post_id,omitempty"`
}

// UpdateProfile applies a partial edit.
func (r *Repo) UpdateProfile(ctx context.Context, userID string, e ProfileEdit) (map[string]string, error) {
	problems := map[string]string{}

	if e.DisplayName != nil {
		*e.DisplayName = strings.TrimSpace(*e.DisplayName)
		if n := len([]rune(*e.DisplayName)); n < 1 || n > 80 {
			problems["display_name"] = "Имя от 1 до 80 символов"
		}
	}
	if e.Bio != nil {
		*e.Bio = strings.TrimSpace(*e.Bio)
		if len([]rune(*e.Bio)) > 1000 {
			problems["bio"] = "Не более 1000 символов"
		}
	}
	if e.Location != nil {
		*e.Location = strings.TrimSpace(*e.Location)
		if len([]rune(*e.Location)) > 80 {
			problems["location"] = "Не более 80 символов"
		}
	}
	if e.Handle != nil {
		*e.Handle = strings.ToLower(strings.TrimSpace(*e.Handle))
		if !isHandle(*e.Handle) {
			problems["handle"] = "От 3 до 30 символов: латиница, цифры и подчёркивание"
		}
	}
	if e.Links != nil {
		clean := make([]author.Link, 0, len(*e.Links))
		for _, l := range *e.Links {
			// Only http and https survive: a javascript: or data: URL rendered
			// as a profile link is a script-injection vector on whichever
			// client opens it.
			if l, ok := author.SanitizeLink(l); ok {
				clean = append(clean, l)
			} else {
				problems["links"] = "Ссылка должна начинаться с http:// или https://"
			}
		}
		if len(clean) > 8 {
			problems["links"] = "Не более 8 ссылок"
		}
		*e.Links = clean
	}
	if len(problems) > 0 {
		return problems, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("studio: profile begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// A featured item must be the author's own and published. Nothing in the
	// schema enforces it, so it is checked on write here and again on read in
	// internal/author -- a moderator can take something down after the fact.
	if e.FeaturedRecipeID != nil && *e.FeaturedRecipeID != "" {
		ok, err := ownsPublished(ctx, tx, "recipes", userID, *e.FeaturedRecipeID)
		if err != nil {
			return nil, err
		}
		if !ok {
			problems["featured_recipe_id"] = "Можно закрепить только свой опубликованный рецепт"
		}
	}
	if e.FeaturedPostID != nil && *e.FeaturedPostID != "" {
		ok, err := ownsPublished(ctx, tx, "posts", userID, *e.FeaturedPostID)
		if err != nil {
			return nil, err
		}
		if !ok {
			problems["featured_post_id"] = "Можно закрепить только свою опубликованную публикацию"
		}
	}
	if len(problems) > 0 {
		return problems, nil
	}

	if e.Handle != nil {
		// A taken handle surfaces as 409, matching what registration already
		// does for the same collision rather than inventing a second shape.
		if err := renameHandle(ctx, tx, userID, *e.Handle); err != nil {
			return nil, err
		}
	}

	// coalesce keeps every untouched column as it was, so one statement covers
	// any subset of the fields.
	if _, err := tx.Exec(ctx, `
		UPDATE users SET
		    display_name       = coalesce($2, display_name),
		    bio                = coalesce($3, bio),
		    location           = coalesce($4, location),
		    links              = coalesce($5::jsonb, links),
		    avatar_media_id    = coalesce(nullif($6,'')::uuid, avatar_media_id),
		    cover_media_id     = coalesce(nullif($7,'')::uuid, cover_media_id),
		    featured_recipe_id = coalesce(nullif($8,'')::uuid, featured_recipe_id),
		    featured_post_id   = coalesce(nullif($9,'')::uuid, featured_post_id)
		 WHERE id = $1::uuid AND deleted_at IS NULL`,
		userID, e.DisplayName, e.Bio, e.Location, linksJSON(e.Links),
		deref(e.AvatarMediaID), deref(e.CoverMediaID),
		deref(e.FeaturedRecipeID), deref(e.FeaturedPostID)); err != nil {
		return nil, fmt.Errorf("studio: update profile: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("studio: profile commit: %w", err)
	}
	return nil, nil
}

// renameHandle records the old handle so links already shared keep working.
func renameHandle(ctx context.Context, tx pgx.Tx, userID, newHandle string) error {
	var current string
	if err := tx.QueryRow(ctx,
		`SELECT handle::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		userID).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("studio: read handle: %w", err)
	}
	if current == newHandle {
		return nil
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET handle = $2 WHERE id = $1::uuid`, userID, newHandle); err != nil {
		if isUniqueViolation(err) {
			return ErrHandleTaken
		}
		return fmt.Errorf("studio: rename: %w", err)
	}
	// Last writer wins: if this handle was in history for someone else, they
	// no longer own the redirect. The live handle always outranks history on
	// read, so this cannot hijack an active page either way.
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_handle_history (old_handle, user_id) VALUES ($2, $1::uuid)
		ON CONFLICT (old_handle) DO UPDATE
		    SET user_id = EXCLUDED.user_id, changed_at = now()`, userID, current); err != nil {
		return fmt.Errorf("studio: record old handle: %w", err)
	}
	// The new handle is live now, so any redirect pointing at it is stale.
	if _, err := tx.Exec(ctx,
		`DELETE FROM user_handle_history WHERE old_handle = $1`, newHandle); err != nil {
		return fmt.Errorf("studio: clear stale redirect: %w", err)
	}
	return nil
}

func ownsPublished(ctx context.Context, tx pgx.Tx, table, userID, id string) (bool, error) {
	// table is one of two literals chosen in this file, never caller input.
	var ok bool
	q := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s
		 WHERE id = $1::uuid AND author_id = $2::uuid
		   AND status = 'published' AND deleted_at IS NULL)`, table)
	if err := tx.QueryRow(ctx, q, id, userID).Scan(&ok); err != nil {
		return false, fmt.Errorf("studio: check featured item: %w", err)
	}
	return ok, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func linksJSON(l *[]author.Link) *string {
	if l == nil {
		return nil
	}
	raw, err := json.Marshal(*l)
	if err != nil {
		return nil
	}
	s := string(raw)
	return &s
}

// isHandle mirrors the users_handle_format CHECK in migration 0001.
func isHandle(s string) bool {
	if len(s) < 3 || len(s) > 30 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
	}
	return true
}
