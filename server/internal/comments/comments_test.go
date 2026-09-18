package comments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/testdb"
)

type stubResolver struct{}

func (stubResolver) PublicURL(key string) string { return "https://cdn.test/" + key }

type rig struct {
	pool   *pgxpool.Pool
	repo   *Repo
	anna   string
	boris  string
	reader string
	recipe string
	post   string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testdb.Pool(t, "comments")
	testdb.Truncate(t, pool, "users", "recipes", "posts", "comments",
		"user_blocks", "user_stats", "reports", "moderation_events")

	ctx := context.Background()
	r := &rig{pool: pool, repo: NewRepo(pool, stubResolver{})}
	mk := func(handle string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (handle, display_name, is_author)
			VALUES ($1, $2, true) RETURNING id::text`, handle, handle).Scan(&id); err != nil {
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
		INSERT INTO posts (author_id, kind, title, slug, status, published_at)
		VALUES ($1::uuid, 'article', 'Статья', 'article', 'published', now()) RETURNING id::text`,
		r.anna).Scan(&r.post); err != nil {
		t.Fatalf("create post: %v", err)
	}
	return r
}

func (r *rig) handler(viewer string) http.Handler {
	mux := http.NewServeMux()
	NewAPI(r.repo, func(*http.Request) string { return viewer }).Routes(mux)
	return mux
}

func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func (r *rig) comment(t *testing.T, viewer, path, body, parent string) string {
	t.Helper()
	payload := fmt.Sprintf(`{"body":%q}`, body)
	if parent != "" {
		payload = fmt.Sprintf(`{"body":%q,"parent_id":%q}`, body, parent)
	}
	rec := do(t, r.handler(viewer), "POST", path, payload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("comment = %d; body: %s", rec.Code, rec.Body.String())
	}
	var out struct{ ID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.ID
}

func TestThreadWithReplies(t *testing.T) {
	r := newRig(t)
	path := "/v1/recipes/" + r.recipe + "/comments"

	root := r.comment(t, r.reader, path, "Отличный рецепт", "")
	r.comment(t, r.anna, path, "Спасибо!", root)
	r.comment(t, r.boris, path, "Присоединяюсь", root)

	rec := do(t, r.handler(r.reader), "GET", path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d; body: %s", rec.Code, rec.Body.String())
	}
	var page Page
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("got %d roots, want 1", len(page.Items))
	}
	if len(page.Items[0].Replies) != 2 {
		t.Errorf("got %d replies, want 2", len(page.Items[0].Replies))
	}
	// The counter tracks visible rows, including replies.
	if page.Total != 3 {
		t.Errorf("total = %d, want 3", page.Total)
	}
	if !page.Items[0].Mine {
		t.Error("viewer's own comment is not marked as theirs")
	}
	// Replies read oldest-first: that is the order a conversation happened in.
	if page.Items[0].Replies[0].Body != "Спасибо!" {
		t.Errorf("replies out of order: %+v", page.Items[0].Replies)
	}
}

// TestReplyToReplyRejected: threads are one level deep, and the database
// enforces it through a composite foreign key rather than a Go check.
func TestReplyToReplyRejected(t *testing.T) {
	r := newRig(t)
	path := "/v1/recipes/" + r.recipe + "/comments"
	root := r.comment(t, r.reader, path, "Корень", "")
	reply := r.comment(t, r.anna, path, "Ответ", root)

	rec := do(t, r.handler(r.boris), "POST", path,
		fmt.Sprintf(`{"body":"Ответ на ответ","parent_id":%q}`, reply))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("= %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

// TestReplyAcrossTargetsRejected: a reply must belong to the same thing its
// parent does, or a comment would appear under a recipe it was never about.
func TestReplyAcrossTargetsRejected(t *testing.T) {
	r := newRig(t)
	root := r.comment(t, r.reader, "/v1/recipes/"+r.recipe+"/comments", "Под рецептом", "")

	rec := do(t, r.handler(r.boris), "POST", "/v1/posts/"+r.post+"/comments",
		fmt.Sprintf(`{"body":"Из другого места","parent_id":%q}`, root))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("= %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

// TestCounterMatchesVisibleRows is the trap in a counter with a partial
// predicate: it must count exactly what the reader sees, or the page says
// "12 комментариев" above nine of them.
func TestCounterMatchesVisibleRows(t *testing.T) {
	r := newRig(t)
	path := "/v1/recipes/" + r.recipe + "/comments"
	a := r.comment(t, r.reader, path, "Первый", "")
	r.comment(t, r.boris, path, "Второй", "")

	if rec := do(t, r.handler(r.reader), "DELETE", "/v1/comments/"+a, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}

	rec := do(t, r.handler(""), "GET", path, "")
	var page Page
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if int(page.Total) != len(page.Items) {
		t.Errorf("counter says %d, page shows %d", page.Total, len(page.Items))
	}
	if page.Total != 1 {
		t.Errorf("total = %d, want 1", page.Total)
	}
}

func TestCannotDeleteSomeoneElsesComment(t *testing.T) {
	r := newRig(t)
	id := r.comment(t, r.reader, "/v1/recipes/"+r.recipe+"/comments", "Мой", "")
	if rec := do(t, r.handler(r.boris), "DELETE", "/v1/comments/"+id, ""); rec.Code != http.StatusNotFound {
		t.Errorf("= %d, want 404", rec.Code)
	}
}

func TestBlockedAuthorsCommentsDisappear(t *testing.T) {
	r := newRig(t)
	path := "/v1/recipes/" + r.recipe + "/comments"
	r.comment(t, r.boris, path, "От Бориса", "")
	r.comment(t, r.anna, path, "От Анны", "")

	if _, err := r.pool.Exec(context.Background(),
		`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1::uuid, $2::uuid)`,
		r.reader, r.boris); err != nil {
		t.Fatalf("block: %v", err)
	}

	rec := do(t, r.handler(r.reader), "GET", path, "")
	var page Page
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, c := range page.Items {
		if c.Author.Handle == "chef_boris" {
			t.Error("a blocked user's comment is still shown")
		}
	}
	if len(page.Items) != 1 {
		t.Errorf("got %d comments, want 1", len(page.Items))
	}
}

func TestRateLimitPerAuthor(t *testing.T) {
	r := newRig(t)
	path := "/v1/recipes/" + r.recipe + "/comments"
	h := r.handler(r.reader)

	for i := range MaxPerMinute {
		rec := do(t, h, "POST", path, fmt.Sprintf(`{"body":"Комментарий %d"}`, i))
		if rec.Code != http.StatusCreated {
			t.Fatalf("comment #%d = %d; body: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := do(t, h, "POST", path, `{"body":"Ещё один"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("= %d, want 429 after %d comments in a minute", rec.Code, MaxPerMinute)
	}
}

func TestAnonymousCanReadButNotWrite(t *testing.T) {
	r := newRig(t)
	path := "/v1/recipes/" + r.recipe + "/comments"
	r.comment(t, r.reader, path, "Видно всем", "")

	if rec := do(t, r.handler(""), "GET", path, ""); rec.Code != http.StatusOK {
		t.Errorf("anonymous read = %d, want 200", rec.Code)
	}
	if rec := do(t, r.handler(""), "POST", path, `{"body":"Аноним"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous write = %d, want 401", rec.Code)
	}
}

func TestCommentOnUnpublishedIsRefused(t *testing.T) {
	r := newRig(t)
	if _, err := r.pool.Exec(context.Background(),
		`UPDATE recipes SET status='draft' WHERE id=$1::uuid`, r.recipe); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	rec := do(t, r.handler(r.reader), "POST", "/v1/recipes/"+r.recipe+"/comments", `{"body":"Ага"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("= %d, want 404", rec.Code)
	}
}

func TestBodyValidation(t *testing.T) {
	r := newRig(t)
	path := "/v1/recipes/" + r.recipe + "/comments"
	h := r.handler(r.reader)

	for name, body := range map[string]string{
		"empty":         `{"body":"   "}`,
		"too long":      fmt.Sprintf(`{"body":%q}`, strings.Repeat("я", MaxBody+1)),
		"bad parent id": `{"body":"текст","parent_id":"nope"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if rec := do(t, h, "POST", path, body); rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("= %d, want 422", rec.Code)
			}
		})
	}
}

func TestKeysetPaging(t *testing.T) {
	r := newRig(t)
	path := "/v1/recipes/" + r.recipe + "/comments"
	for i := range 5 {
		// Spread authors so the per-author rate limit does not bite.
		author := []string{r.reader, r.boris, r.anna}[i%3]
		r.comment(t, author, path, fmt.Sprintf("Комментарий %d", i), "")
	}

	h := r.handler("")
	rec := do(t, h, "GET", path+"?limit=2", "")
	var first Page
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(first.Items) != 2 || !first.HasMore {
		t.Fatalf("first page = %d items, hasMore=%v", len(first.Items), first.HasMore)
	}

	rec = do(t, h, "GET", path+"?limit=2&cursor="+url.QueryEscape(first.NextCursor), "")
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
			t.Errorf("page 2 repeats a comment from page 1: %q", c.Body)
		}
	}
}

func TestDeletingRecipeTakesCommentsWithIt(t *testing.T) {
	r := newRig(t)
	r.comment(t, r.reader, "/v1/recipes/"+r.recipe+"/comments", "Исчезнет", "")

	if _, err := r.pool.Exec(context.Background(),
		`DELETE FROM recipes WHERE id=$1::uuid`, r.recipe); err != nil {
		t.Fatalf("delete recipe: %v", err)
	}
	var left int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM comments`).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Errorf("%d comments orphaned by deleting their recipe", left)
	}
}
