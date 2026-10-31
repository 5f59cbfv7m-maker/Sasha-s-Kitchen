package storefront

import (
	"context"
	"testing"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/testdb"
)

type cdn struct{}

func (cdn) PublicURL(key string) string { return "https://cdn.test/" + key }

// A store with published recipes but no editorial collections showed an empty
// front page.
func TestFrontFallsBackToAutomaticShelves(t *testing.T) {
	pool := testdb.Pool(t, "storefront")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE users, recipes, collections RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		WITH u AS (INSERT INTO users (handle, display_name) VALUES ('cook', 'Cook') RETURNING id)
		INSERT INTO recipes (author_id, slug, title, status, published_at, cook_time_minutes)
		SELECT u.id, s, s, 'published', now(), m FROM u, (VALUES ('soup', 40), ('salad', 10)) v(s, m)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	repo := NewRepo(pool, search.NewRepo(pool, cdn{}), cdn{})

	front, err := repo.Front(ctx, "", 10)
	if err != nil {
		t.Fatalf("Front: %v", err)
	}
	got := map[string]int{}
	for _, s := range front.Shelves {
		got[s.Slug] = len(s.Items)
	}
	if got["new"] != 2 || got["popular"] != 2 || got["quick"] != 2 {
		t.Fatalf("shelves = %v, want new, popular and quick with both recipes", got)
	}
	if front.Shelves[2].Items[0].Title != "salad" {
		t.Errorf("quick shelf starts with %q, want the 10-minute salad", front.Shelves[2].Items[0].Title)
	}
}
