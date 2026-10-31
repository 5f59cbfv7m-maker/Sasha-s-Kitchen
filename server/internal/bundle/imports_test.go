package bundle

import (
	"context"
	"testing"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/testdb"
)

// The storefront records an import with ON CONFLICT DO NOTHING against this
// index; a retried fetch or a re-opened recipe must not count twice.
func TestImportCountedOncePerPerson(t *testing.T) {
	pool := testdb.Pool(t, "bundle")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE users, recipes RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	var user, recipe string
	if err := pool.QueryRow(ctx, `INSERT INTO users (handle, display_name) VALUES ('cook', 'Cook') RETURNING id::text`).Scan(&user); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO recipes (author_id, slug, title, status, published_at)
		VALUES ($1::uuid, 'soup', 'Суп', 'published', now()) RETURNING id::text`, user).Scan(&recipe); err != nil {
		t.Fatalf("recipe: %v", err)
	}
	for range 3 {
		if _, err := pool.Exec(ctx, `
			INSERT INTO recipe_imports (user_id, recipe_id) VALUES ($1::uuid, $2::uuid)
			ON CONFLICT (user_id, recipe_id) WHERE user_id IS NOT NULL DO NOTHING`, user, recipe); err != nil {
			t.Fatalf("import: %v", err)
		}
	}
	var count int64
	if err := pool.QueryRow(ctx, `SELECT import_count FROM recipes WHERE id = $1::uuid`, recipe).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("import_count = %d, want 1", count)
	}
}

type cdn struct{}

func (cdn) PublicURL(key string) string { return "https://cdn.test/" + key }

// An imported recipe used to arrive without its picture although the app
// shows a photo on every recipe card.
func TestBundleCarriesHeroPhoto(t *testing.T) {
	pool := testdb.Pool(t, "bundle")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE users, recipes, media_assets RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	var user, recipe, photo, video string
	if err := pool.QueryRow(ctx, `INSERT INTO users (handle, display_name) VALUES ('cook', 'Cook') RETURNING id::text`).Scan(&user); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO recipes (author_id, slug, title, status, published_at)
		VALUES ($1::uuid, 'soup', 'Суп', 'published', now()) RETURNING id::text`, user).Scan(&recipe); err != nil {
		t.Fatalf("recipe: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO recipe_ingredients (recipe_id, product_name, amount_per_base_serving, unit)
		VALUES ($1::uuid, 'Картофель', 300, 'г')`, recipe); err != nil {
		t.Fatalf("ingredient: %v", err)
	}
	for _, m := range []struct {
		kind, key string
		id        *string
	}{{"video", "orig/v.mp4", &video}, {"photo", "photo/soup.jpg", &photo}} {
		if err := pool.QueryRow(ctx, `
			INSERT INTO media_assets (owner_id, kind, status, storage_key, poster_key)
			VALUES ($1::uuid, $2, 'ready', $3, 'poster/v.jpg') RETURNING id::text`, user, m.kind, m.key).Scan(m.id); err != nil {
			t.Fatalf("media: %v", err)
		}
	}
	// The photo comes second but is the hero; the video's poster must not win.
	if _, err := pool.Exec(ctx, `
		INSERT INTO recipe_media (recipe_id, media_id, position, role)
		VALUES ($1::uuid, $2::uuid, 0, 'step'), ($1::uuid, $3::uuid, 1, 'hero')`, recipe, video, photo); err != nil {
		t.Fatalf("recipe media: %v", err)
	}

	b, err := NewRepo(pool, cdn{}).Build(ctx, recipe, "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if b.Recipe.PhotoURL != "https://cdn.test/photo/soup.jpg" {
		t.Errorf("photo_url = %q", b.Recipe.PhotoURL)
	}
}
