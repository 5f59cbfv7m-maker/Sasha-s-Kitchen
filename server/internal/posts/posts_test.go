package posts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/postgres"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping database-backed test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	target := strings.TrimPrefix(u.Path, "/") + "_posts"

	admin := *u
	admin.Path = "/postgres"
	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		t.Fatalf("connect to maintenance database: %v", err)
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, `CREATE DATABASE "`+target+`"`); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create database: %v", err)
	}

	out := *u
	out.Path = "/" + target
	pool, err := pgxpool.New(ctx, out.String())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := postgres.Migrate(ctx, pool, quiet); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type stubResolver struct{}

func (stubResolver) PublicURL(key string) string { return "https://cdn.test/" + key }

type rig struct {
	pool   *pgxpool.Pool
	repo   *Repo
	anna   string
	boris  string
	reader string
	recipe string
	video  string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		TRUNCATE users, recipes, posts, media_assets, user_blocks, user_stats
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	searchRepo := search.NewRepo(pool, stubResolver{})
	r := &rig{pool: pool, repo: NewRepo(pool, searchRepo, stubResolver{})}

	mk := func(handle string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (handle, display_name, is_author, trust_level)
			VALUES ($1, $2, true, 1) RETURNING id::text`, handle, handle).Scan(&id); err != nil {
			t.Fatalf("create user: %v", err)
		}
		return id
	}
	r.anna, r.boris, r.reader = mk("chef_anna"), mk("chef_boris"), mk("just_reader")

	if err := pool.QueryRow(ctx, `
		INSERT INTO recipes (author_id, slug, title, status, published_at)
		VALUES ($1::uuid, 'borshch', 'Борщ', 'published', now()) RETURNING id::text`,
		r.anna).Scan(&r.recipe); err != nil {
		t.Fatalf("create recipe: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_assets (owner_id, kind, status, storage_key, poster_key, hls_key)
		VALUES ($1::uuid, 'video', 'ready', 'orig/v.mp4', 'poster/v.jpg', 'hls/v.m3u8')
		RETURNING id::text`, r.anna).Scan(&r.video); err != nil {
		t.Fatalf("create media: %v", err)
	}
	return r
}

func (r *rig) article(t *testing.T, authorID, slug string, blocks []Block, minutesAgo int) string {
	t.Helper()
	raw, err := json.Marshal(blocks)
	if err != nil {
		t.Fatalf("marshal blocks: %v", err)
	}
	var id string
	if err := r.pool.QueryRow(context.Background(), `
		INSERT INTO posts (author_id, kind, title, slug, body_blocks, status, published_at)
		VALUES ($1::uuid, 'article', $2, $3, $4::jsonb, 'published', now() - make_interval(mins => $5))
		RETURNING id::text`, authorID, "Статья "+slug, slug, string(raw), int32(minutesAgo)).Scan(&id); err != nil {
		t.Fatalf("create article: %v", err)
	}
	return id
}

func (r *rig) short(t *testing.T, authorID, title string, ready bool, minutesAgo int) string {
	t.Helper()
	ctx := context.Background()
	var mediaID string
	status := "processing"
	if ready {
		status = "ready"
	}
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO media_assets (owner_id, kind, status, storage_key, poster_key, hls_key)
		VALUES ($1::uuid, 'video', $2, 'orig/x.mp4', 'poster/x.jpg', 'hls/x.m3u8')
		RETURNING id::text`, authorID, status).Scan(&mediaID); err != nil {
		t.Fatalf("create short media: %v", err)
	}
	var id string
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO posts (author_id, kind, title, video_media_id, video_ready, status, published_at)
		VALUES ($1::uuid, 'short', $2, $3::uuid, $4, 'published', now() - make_interval(mins => $5))
		RETURNING id::text`, authorID, title, mediaID, ready, int32(minutesAgo)).Scan(&id); err != nil {
		t.Fatalf("create short: %v", err)
	}
	return id
}

func (r *rig) handler(viewer string) http.Handler {
	mux := http.NewServeMux()
	NewAPI(r.repo, func(*http.Request) string { return viewer }).Routes(mux)
	return mux
}

func do(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
	return rec
}

func TestArticleRendersBlocks(t *testing.T) {
	r := newRig(t)
	id := r.article(t, r.anna, "borshch", []Block{
		{Type: BlockParagraph, Text: "Вступление"},
		{Type: BlockPhoto, MediaID: r.video, Caption: "Кадр"},
		{Type: BlockRecipe, RecipeID: r.recipe},
	}, 1)

	rec := do(t, r.handler(""), "/v1/posts/"+id)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var d Detail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(d.Blocks) != 3 {
		t.Fatalf("got %d blocks, want 3: %+v", len(d.Blocks), d.Blocks)
	}
	if len(d.Blocks[1].Media) != 1 || d.Blocks[1].Media[0].URL == "" {
		t.Errorf("photo block did not resolve to a URL: %+v", d.Blocks[1])
	}
	// The embedded recipe is the point of the whole feature: a reader takes it
	// straight out of the article.
	if d.Blocks[2].Recipe == nil || d.Blocks[2].Recipe.Title != "Борщ" {
		t.Errorf("recipe block did not resolve: %+v", d.Blocks[2])
	}
	// Storage keys are internal and must never reach a client.
	if strings.Contains(rec.Body.String(), `"storage_key"`) {
		t.Error("response leaks storage keys")
	}
}

// TestUnpublishedRecipeVanishesFromBody: embedding must not become a way to
// show a recipe its author has not released.
func TestUnpublishedRecipeVanishesFromBody(t *testing.T) {
	r := newRig(t)
	id := r.article(t, r.anna, "draft-ref", []Block{
		{Type: BlockParagraph, Text: "Текст"},
		{Type: BlockRecipe, RecipeID: r.recipe},
	}, 1)
	if _, err := r.pool.Exec(context.Background(),
		`UPDATE recipes SET status='draft' WHERE id=$1::uuid`, r.recipe); err != nil {
		t.Fatalf("unpublish: %v", err)
	}

	rec := do(t, r.handler(""), "/v1/posts/"+id)
	var d Detail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, b := range d.Blocks {
		if b.Type == BlockRecipe {
			t.Fatal("an unpublished recipe is still embedded in a published article")
		}
	}
	if len(d.Blocks) != 1 {
		t.Errorf("got %d blocks, want just the paragraph", len(d.Blocks))
	}
}

func TestShortsFeedExcludesUnreadyVideo(t *testing.T) {
	r := newRig(t)
	r.short(t, r.anna, "Готовый шортс", true, 1)
	r.short(t, r.anna, "Ещё кодируется", false, 2)

	rec := do(t, r.handler(""), "/v1/shorts")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var page Page
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "Готовый шортс" {
		t.Fatalf("feed = %+v; a short whose HLS is not ready is a dead card", page.Items)
	}
	if page.Items[0].Media == nil || page.Items[0].Media.URL == "" {
		t.Error("short card carries no playable URL")
	}
}

func TestShortsFeedKeysetPaging(t *testing.T) {
	r := newRig(t)
	for i := range 5 {
		r.short(t, r.anna, fmt.Sprintf("Шортс %d", i), true, i)
	}

	h := r.handler("")
	rec := do(t, h, "/v1/shorts?limit=2")
	var first Page
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(first.Items) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page = %d items, hasMore=%v, cursor=%q",
			len(first.Items), first.HasMore, first.NextCursor)
	}

	rec = do(t, h, "/v1/shorts?limit=2&cursor="+url.QueryEscape(first.NextCursor))
	var second Page
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range first.Items {
		seen[c.ID] = true
	}
	for _, c := range second.Items {
		if seen[c.ID] {
			t.Errorf("page 2 repeats %q from page 1", c.Title)
		}
	}
}

// TestCursorFromAnotherSortRejected: paging one ordering with another's cursor
// silently skips and repeats rows, which on a vertical feed is exactly the
// failure a reader notices.
func TestCursorFromAnotherSortRejected(t *testing.T) {
	r := newRig(t)
	for i := range 3 {
		r.short(t, r.anna, fmt.Sprintf("Шортс %d", i), true, i)
	}
	h := r.handler("")

	rec := do(t, h, "/v1/shorts?limit=1")
	var page Page
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rec = do(t, h, "/v1/shorts?limit=1&sort=top&cursor="+url.QueryEscape(page.NextCursor))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("cursor from another sort = %d, want 422", rec.Code)
	}
}

func TestBlockedAuthorDisappearsFromFeedAndPost(t *testing.T) {
	r := newRig(t)
	r.short(t, r.anna, "Шортс Анны", true, 1)
	r.short(t, r.boris, "Шортс Бориса", true, 2)
	id := r.article(t, r.anna, "anna-post", []Block{{Type: BlockParagraph, Text: "Текст"}}, 1)

	if _, err := r.pool.Exec(context.Background(),
		`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1::uuid, $2::uuid)`,
		r.reader, r.anna); err != nil {
		t.Fatalf("block: %v", err)
	}

	rec := do(t, r.handler(r.reader), "/v1/shorts")
	var page Page
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "Шортс Бориса" {
		t.Errorf("blocked author still in feed: %+v", page.Items)
	}
	if rec := do(t, r.handler(r.reader), "/v1/posts/"+id); rec.Code != http.StatusNotFound {
		t.Errorf("blocked author's post = %d, want 404", rec.Code)
	}
	// Anonymous readers and everyone else are unaffected.
	if rec := do(t, r.handler(""), "/v1/posts/"+id); rec.Code != http.StatusOK {
		t.Errorf("anonymous view of the same post = %d, want 200", rec.Code)
	}
}

func TestDraftPostIsNotPublic(t *testing.T) {
	r := newRig(t)
	id := r.article(t, r.anna, "wip", []Block{{Type: BlockParagraph, Text: "Черновик"}}, 1)
	if _, err := r.pool.Exec(context.Background(),
		`UPDATE posts SET status='draft', published_at=NULL WHERE id=$1::uuid`, id); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if rec := do(t, r.handler(""), "/v1/posts/"+id); rec.Code != http.StatusNotFound {
		t.Errorf("draft = %d, want 404", rec.Code)
	}
}

func TestValidateRefsGuardsOwnershipAndPublication(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()

	// Someone else's asset must not be embeddable.
	problems, err := r.repo.ValidateRefs(ctx, r.boris, []Block{{Type: BlockPhoto, MediaID: r.video}})
	if err != nil {
		t.Fatalf("ValidateRefs: %v", err)
	}
	if len(problems) == 0 {
		t.Error("an author embedded someone else's media and it was accepted")
	}

	// The owner may embed their own.
	if problems, err = r.repo.ValidateRefs(ctx, r.anna, []Block{{Type: BlockPhoto, MediaID: r.video}}); err != nil {
		t.Fatalf("ValidateRefs: %v", err)
	}
	if len(problems) != 0 {
		t.Errorf("owner rejected from their own media: %v", problems)
	}

	// An unpublished recipe must not be embeddable.
	if _, err := r.pool.Exec(ctx, `UPDATE recipes SET status='draft' WHERE id=$1::uuid`, r.recipe); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if problems, err = r.repo.ValidateRefs(ctx, r.anna, []Block{{Type: BlockRecipe, RecipeID: r.recipe}}); err != nil {
		t.Fatalf("ValidateRefs: %v", err)
	}
	if len(problems) == 0 {
		t.Error("a draft recipe was accepted as an embed")
	}

	if problems, err = r.repo.ValidateRefs(ctx, r.anna,
		[]Block{{Type: BlockRecipe, RecipeID: uuid.NewString()}}); err != nil {
		t.Fatalf("ValidateRefs: %v", err)
	}
	if len(problems) == 0 {
		t.Error("a nonexistent recipe was accepted as an embed")
	}
}
