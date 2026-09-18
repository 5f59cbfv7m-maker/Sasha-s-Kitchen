package author

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/testdb"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/posts"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
)

// testPool gives this package its own database, as internal/search does: these
// tests TRUNCATE, and `go test ./...` runs packages in parallel.
func testPool(t *testing.T) *pgxpool.Pool {
	return testdb.Pool(t, "author")
}

// stubResolver stands in for the CDN, as in internal/search.
type stubResolver struct{}

func (stubResolver) PublicURL(key string) string { return "https://cdn.test/" + key }

type rig struct {
	pool   *pgxpool.Pool
	repo   *Repo
	search *search.Repo
	posts  *posts.Repo
	anna   string // author with recipes
	boris  string // another author
	reader string // plain reader
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		TRUNCATE users, recipes, media_assets, recipe_media, follows,
		         user_blocks, user_handle_history, user_stats, posts
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	searchRepo := search.NewRepo(pool, stubResolver{})
	r := &rig{pool: pool, search: searchRepo,
		posts: posts.NewRepo(pool, searchRepo, stubResolver{}),
		repo:  NewRepo(pool, searchRepo, stubResolver{})}

	r.anna = r.user(t, "chef_anna", "Анна", true)
	r.boris = r.user(t, "chef_boris", "Борис", true)
	r.reader = r.user(t, "just_reader", "Читатель", false)

	for i, title := range []string{"Борщ", "Блины", "Омлет"} {
		r.recipe(t, r.anna, fmt.Sprintf("anna-%d", i), title)
	}
	r.recipe(t, r.boris, "boris-0", "Тирамису")
	return r
}

func (r *rig) user(t *testing.T, handle, name string, isAuthor bool) string {
	t.Helper()
	var id string
	if err := r.pool.QueryRow(context.Background(), `
		INSERT INTO users (handle, display_name, is_author, trust_level)
		VALUES ($1, $2, $3, 1) RETURNING id::text`, handle, name, isAuthor).Scan(&id); err != nil {
		t.Fatalf("create user %s: %v", handle, err)
	}
	return id
}

func (r *rig) recipe(t *testing.T, authorID, slug, title string) string {
	t.Helper()
	var id string
	if err := r.pool.QueryRow(context.Background(), `
		INSERT INTO recipes (author_id, slug, title, status, published_at)
		VALUES ($1::uuid, $2, $3, 'published', now() - make_interval(mins => $4))
		RETURNING id::text`, authorID, slug, title, int32(len(slug))).Scan(&id); err != nil {
		t.Fatalf("create recipe %s: %v", slug, err)
	}
	return id
}

func (r *rig) handler(viewer string) http.Handler {
	mux := http.NewServeMux()
	NewAPI(r.repo, r.search, r.posts, func(*http.Request) string { return viewer }).Routes(mux)
	return mux
}

func do(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return id
}
