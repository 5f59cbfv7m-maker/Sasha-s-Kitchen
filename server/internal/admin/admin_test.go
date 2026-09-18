package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/moderation"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/testdb"
)

type rig struct {
	pool *pgxpool.Pool
	mod  *moderation.Repo
	mod2 uuid.UUID // the moderator
	anna uuid.UUID // a newcomer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testdb.Pool(t, "admin")
	testdb.Truncate(t, pool, "users", "recipes", "posts", "reports",
		"moderation_events", "user_stats")

	ctx := context.Background()
	r := &rig{pool: pool, mod: moderation.NewRepo(pool)}
	mk := func(handle string, admin bool) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (handle, display_name, is_author, is_admin, trust_level)
			VALUES ($1, $2, true, $3, 0) RETURNING id`, handle, handle, admin).Scan(&id); err != nil {
			t.Fatalf("create user: %v", err)
		}
		return id
	}
	r.mod2, r.anna = mk("moderator", true), mk("chef_anna", false)
	return r
}

func (r *rig) recipe(t *testing.T, slug, status string) string {
	t.Helper()
	var id string
	published := "NULL"
	if status == "published" {
		published = "now()"
	}
	if err := r.pool.QueryRow(context.Background(), fmt.Sprintf(`
		INSERT INTO recipes (author_id, slug, title, status, published_at)
		VALUES ($1, $2, $3, $4, %s) RETURNING id::text`, published),
		r.anna, slug, "Рецепт "+slug, status).Scan(&id); err != nil {
		t.Fatalf("create recipe: %v", err)
	}
	return id
}

func (r *rig) handler(caller uuid.UUID, isAdmin bool) http.Handler {
	mux := http.NewServeMux()
	NewAPI(r.mod, func(*http.Request) (uuid.UUID, bool) { return caller, isAdmin }).Routes(mux)
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

// TestTrustLadderPromotes is the rule the whole moderation policy rests on:
// a newcomer is premoderated until enough of their work has been approved,
// after which they publish directly. Without it the queue grows with every
// post rather than with every new author, and a human has to keep up forever.
func TestTrustLadderPromotes(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.mod2, true)
	ctx := context.Background()

	for i := range moderation.PromoteAfter {
		id := r.recipe(t, fmt.Sprintf("r%d", i), "review")
		if rec := do(t, h, "POST", "/v1/admin/content/recipe/"+id+"/approve", ""); rec.Code != http.StatusNoContent {
			t.Fatalf("approve #%d = %d; body: %s", i+1, rec.Code, rec.Body.String())
		}

		level, approved, err := r.mod.TrustOf(ctx, r.anna.String())
		if err != nil {
			t.Fatalf("TrustOf: %v", err)
		}
		if approved != i+1 {
			t.Errorf("approved count = %d, want %d", approved, i+1)
		}
		want := moderation.TrustNewcomer
		if i+1 >= moderation.PromoteAfter {
			want = moderation.TrustTrusted
		}
		if level != want {
			t.Errorf("after %d approvals trust = %d, want %d", i+1, level, want)
		}
	}
}

// TestTakedownDemotes is the other half: "publish first, moderate on report"
// is only survivable if being caught costs the author their direct access.
func TestTakedownDemotes(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.mod2, true)
	ctx := context.Background()

	if _, err := r.pool.Exec(ctx,
		`UPDATE users SET trust_level = $2 WHERE id = $1`, r.anna, moderation.TrustTrusted); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE user_stats SET approved_count = 5 WHERE user_id = $1`, r.anna); err != nil {
		t.Fatalf("seed approvals: %v", err)
	}
	id := r.recipe(t, "bad", "published")

	rec := do(t, h, "POST", "/v1/admin/content/recipe/"+id+"/takedown", `{"reason":"Небезопасно"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("takedown = %d; body: %s", rec.Code, rec.Body.String())
	}

	level, approved, err := r.mod.TrustOf(ctx, r.anna.String())
	if err != nil {
		t.Fatalf("TrustOf: %v", err)
	}
	if level != moderation.TrustRestricted {
		t.Errorf("trust after takedown = %d, want %d", level, moderation.TrustRestricted)
	}
	// Progress resets: trust has to be earned again, not resumed from where it
	// stood when the violation happened.
	if approved != 0 {
		t.Errorf("approved count after takedown = %d, want 0", approved)
	}
}

// TestVerificationSurvivesAutomation: verification is a human decision, so an
// automatic demotion must not quietly undo it.
func TestVerificationSurvivesAutomation(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.mod2, true)
	ctx := context.Background()

	if rec := do(t, h, "POST", "/v1/admin/users/"+r.anna.String()+"/verify", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("verify = %d; body: %s", rec.Code, rec.Body.String())
	}
	id := r.recipe(t, "bad", "published")
	if rec := do(t, h, "POST", "/v1/admin/content/recipe/"+id+"/takedown", `{"reason":"Спорно"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("takedown = %d", rec.Code)
	}

	level, _, err := r.mod.TrustOf(ctx, r.anna.String())
	if err != nil {
		t.Fatalf("TrustOf: %v", err)
	}
	if level != moderation.TrustVerified {
		t.Errorf("verified author demoted automatically to %d", level)
	}
}

// TestReapprovalKeepsOriginalDate: a fixed typo must not send a recipe back to
// the top of the "new" shelf.
func TestReapprovalKeepsOriginalDate(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.mod2, true)
	ctx := context.Background()

	id := r.recipe(t, "keeper", "review")
	if rec := do(t, h, "POST", "/v1/admin/content/recipe/"+id+"/approve", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("approve = %d", rec.Code)
	}
	var first string
	if err := r.pool.QueryRow(ctx,
		`SELECT published_at::text FROM recipes WHERE id=$1::uuid`, id).Scan(&first); err != nil {
		t.Fatalf("read: %v", err)
	}

	if _, err := r.pool.Exec(ctx, `UPDATE recipes SET status='review' WHERE id=$1::uuid`, id); err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if rec := do(t, h, "POST", "/v1/admin/content/recipe/"+id+"/approve", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("re-approve = %d", rec.Code)
	}
	var second string
	if err := r.pool.QueryRow(ctx,
		`SELECT published_at::text FROM recipes WHERE id=$1::uuid`, id).Scan(&second); err != nil {
		t.Fatalf("read: %v", err)
	}
	if first != second {
		t.Errorf("published_at moved on re-approval: %s -> %s", first, second)
	}
}

func TestQueueCoversRecipesAndPosts(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.recipe(t, "waiting", "review")
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO posts (author_id, kind, title, slug, status)
		VALUES ($1, 'article', 'Статья на проверке', 'article-review', 'review')`, r.anna); err != nil {
		t.Fatalf("create post: %v", err)
	}

	rec := do(t, r.handler(r.mod2, true), "GET", "/v1/admin/queue", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d; body: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Items []moderation.Item `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	kinds := map[string]bool{}
	for _, it := range out.Items {
		kinds[it.Kind] = true
	}
	if !kinds["recipe"] || !kinds["post"] {
		t.Errorf("queue covers %v, want both recipe and post", kinds)
	}
}

// TestNonModeratorSeesNothing: the moderator surface answers 404 rather than
// 403, because confirming it exists is itself information.
func TestNonModeratorSeesNothing(t *testing.T) {
	r := newRig(t)
	for _, h := range []http.Handler{
		r.handler(r.anna, false),
		r.handler(uuid.UUID{}, false),
	} {
		for _, target := range []string{"/v1/admin/queue", "/v1/admin/reports"} {
			if rec := do(t, h, "GET", target, ""); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s as non-moderator = %d, want 404", target, rec.Code)
			}
		}
		rec := do(t, h, "POST", "/v1/admin/users/"+uuid.NewString()+"/suspend", `{"reason":"x"}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("suspend as non-moderator = %d, want 404", rec.Code)
		}
	}
}

// TestRejectRequiresReason: an author sent a rejection with no explanation has
// nothing to act on.
func TestRejectRequiresReason(t *testing.T) {
	r := newRig(t)
	id := r.recipe(t, "why", "review")
	rec := do(t, r.handler(r.mod2, true), "POST", "/v1/admin/content/recipe/"+id+"/reject", `{}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("= %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

func TestTakedownResolvesOpenReports(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	id := r.recipe(t, "reported", "published")

	if _, err := r.mod.Report(ctx, r.mod2.String(), moderation.ReportInput{
		TargetType: "recipe", TargetID: id, Reason: "spam",
	}); err != nil {
		t.Fatalf("report: %v", err)
	}
	rec := do(t, r.handler(r.mod2, true), "POST",
		"/v1/admin/content/recipe/"+id+"/takedown", `{"reason":"Спам"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("takedown = %d; body: %s", rec.Code, rec.Body.String())
	}

	var open int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM reports WHERE status IN ('open','reviewing')`).Scan(&open); err != nil {
		t.Fatalf("count: %v", err)
	}
	if open != 0 {
		t.Errorf("%d reports left open about content already taken down", open)
	}
}

func TestApproveWrongStateIsConflict(t *testing.T) {
	r := newRig(t)
	id := r.recipe(t, "draft-one", "draft")
	rec := do(t, r.handler(r.mod2, true), "POST", "/v1/admin/content/recipe/"+id+"/approve", "")
	if rec.Code != http.StatusConflict {
		t.Errorf("approving a draft = %d, want 409", rec.Code)
	}
}
