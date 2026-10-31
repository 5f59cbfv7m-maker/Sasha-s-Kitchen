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
