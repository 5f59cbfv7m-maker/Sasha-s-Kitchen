package posts

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
)

// renderBlocks resolves the ids in a stored body into things a client can
// display: media URLs and recipe cards.
//
// References are fetched in two batched queries rather than one per block. A
// twenty-photo gallery post should not cost twenty round trips, and an article
// that embeds several recipes should not cost several more.
func (r *Repo) renderBlocks(ctx context.Context, blocks []Block, viewerID string) ([]RenderedBlock, error) {
	mediaIDs, recipeIDs := refsOf(blocks)

	media, err := r.loadMedia(ctx, mediaIDs)
	if err != nil {
		return nil, err
	}
	recipes, err := r.loadRecipes(ctx, recipeIDs, viewerID)
	if err != nil {
		return nil, err
	}

	out := make([]RenderedBlock, 0, len(blocks))
	for _, b := range blocks {
		rb := RenderedBlock{
			Type: b.Type, Text: b.Text, Level: b.Level,
			Ordered: b.Ordered, Items: b.Items, Caption: b.Caption,
		}
		switch b.Type {
		case BlockPhoto, BlockVideo, BlockGallery:
			for _, id := range b.MediaRefs() {
				// A reference that did not resolve is skipped rather than
				// rendered as a broken tile: the asset may still be
				// transcoding, or a moderator may have taken it down.
				if m, ok := media[id]; ok {
					rb.Media = append(rb.Media, m)
				}
			}
			if len(rb.Media) == 0 {
				continue
			}
		case BlockRecipe:
			c, ok := recipes[b.RecipeID]
			if !ok {
				// Unpublished, deleted, or by an author this viewer blocked.
				continue
			}
			rb.Recipe = c
		}
		out = append(out, rb)
	}
	return out, nil
}

func refsOf(blocks []Block) (media, recipes []string) {
	seenM, seenR := map[string]bool{}, map[string]bool{}
	for _, b := range blocks {
		for _, id := range b.MediaRefs() {
			if !seenM[id] {
				seenM[id] = true
				media = append(media, id)
			}
		}
		if b.Type == BlockRecipe && b.RecipeID != "" && !seenR[b.RecipeID] {
			seenR[b.RecipeID] = true
			recipes = append(recipes, b.RecipeID)
		}
	}
	return media, recipes
}

func (r *Repo) loadMedia(ctx context.Context, ids []string) (map[string]search.Media, error) {
	out := map[string]search.Media{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, kind, storage_key, poster_key, hls_key, blurhash, width, height
		  FROM media_assets
		 WHERE id = ANY($1::uuid[]) AND status = 'ready'`, ids)
	if err != nil {
		return nil, fmt.Errorf("posts: load block media: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id, kind              string
			storageKey, posterKey pgtype.Text
			hlsKey, blurhash      pgtype.Text
			width, height         pgtype.Int4
		)
		if err := rows.Scan(&id, &kind, &storageKey, &posterKey, &hlsKey,
			&blurhash, &width, &height); err != nil {
			return nil, fmt.Errorf("posts: scan block media: %w", err)
		}
		m := resolveMedia(r.media, kind, storageKey, posterKey, hlsKey, blurhash, width, height)
		m.ID = id
		out[id] = *m
	}
	return out, rows.Err()
}

// loadRecipes fetches embedded recipe cards through the storefront query
// engine, so an embedded card obeys the same published, active-author and
// block rules as the same recipe anywhere else in the store.
func (r *Repo) loadRecipes(ctx context.Context, ids []string, viewerID string) (map[string]*search.Card, error) {
	out := map[string]*search.Card{}
	for _, id := range ids {
		card, err := r.search.ListOne(ctx, search.Query{}, id, viewerID)
		if err != nil {
			return nil, err
		}
		if card != nil {
			out[id] = card
		}
	}
	return out, nil
}

// ValidateRefs is the half of body validation that needs the database.
//
// Two rules, both of which exist because a post body is the one place where an
// author supplies raw identifiers:
//
//  1. Every media id must belong to this author. Without the check, anyone
//     could paste someone else's asset id and publish their unreleased photo.
//  2. Every embedded recipe must be published. Embedding a draft would leak a
//     recipe before its author meant to release it.
//
// Media is NOT required to be 'ready': an author assembles a post while the
// video is still transcoding. The read path skips what is not ready yet, and
// the feed indexes keep unready shorts out until they are.
func (r *Repo) ValidateRefs(ctx context.Context, authorID string, blocks []Block) (map[string]string, error) {
	mediaIDs, recipeIDs := refsOf(blocks)
	problems := map[string]string{}

	if len(mediaIDs) > 0 {
		var ownedCount int
		if err := r.pool.QueryRow(ctx, `
			SELECT count(*) FROM media_assets
			 WHERE id = ANY($1::uuid[]) AND owner_id = $2::uuid`,
			mediaIDs, authorID).Scan(&ownedCount); err != nil {
			return nil, fmt.Errorf("posts: verify media ownership: %w", err)
		}
		if ownedCount != len(mediaIDs) {
			problems["blocks"] = "В тексте есть файлы, которые вам не принадлежат"
		}
	}

	if len(recipeIDs) > 0 {
		var publishedCount int
		if err := r.pool.QueryRow(ctx, `
			SELECT count(*) FROM recipes
			 WHERE id = ANY($1::uuid[]) AND status = 'published' AND deleted_at IS NULL`,
			recipeIDs).Scan(&publishedCount); err != nil {
			return nil, fmt.Errorf("posts: verify recipe refs: %w", err)
		}
		if publishedCount != len(recipeIDs) {
			problems["blocks"] = "В тексте есть рецепты, которых нет или которые не опубликованы"
		}
	}

	if len(problems) == 0 {
		return nil, nil
	}
	return problems, nil
}
