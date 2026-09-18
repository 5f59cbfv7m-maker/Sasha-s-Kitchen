package social

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

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/testdb"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/moderation"
)

// testPool gives this package its own database, as internal/search does, so
// parallel package runs cannot tread on each other's rows.
func testPool(t *testing.T) *pgxpool.Pool {
	return testdb.Pool(t, "social")
}

type rig struct {
	pool   *pgxpool.Pool
	viewer uuid.UUID
	other  uuid.UUID
	recipe uuid.UUID
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `TRUNCATE users, recipes, reports, user_blocks CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	mkUser := func(handle string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (handle, display_name) VALUES ($1, $2) RETURNING id`,
			handle, "Автор "+handle).Scan(&id); err != nil {
			t.Fatalf("create user %s: %v", handle, err)
		}
		return id
	}
	r := &rig{pool: pool, viewer: mkUser("viewer_one"), other: mkUser("author_two")}

	if err := pool.QueryRow(ctx, `
		INSERT INTO recipes (author_id, slug, title, status, published_at)
		VALUES ($1, 'test-recipe', 'Тестовый рецепт', 'published', now())
		RETURNING id`, r.other).Scan(&r.recipe); err != nil {
		t.Fatalf("create recipe: %v", err)
	}
	return r
}

func (r *rig) handler(caller uuid.UUID, authed bool) http.Handler {
	mux := http.NewServeMux()
	NewAPI(moderation.NewRepo(r.pool), func(*http.Request) (uuid.UUID, bool) {
		return caller, authed
	}).Routes(mux)
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

// TestBlockRoundTrip is the Guideline 1.2 path end to end: block, see the
// block listed, unblock. None of this was reachable before: the repository
// methods existed but registered no routes.
func TestBlockRoundTrip(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.viewer, true)

	if rec := do(t, h, "PUT", "/v1/blocks/"+r.other.String(), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d, want 204; body: %s", rec.Code, rec.Body.String())
	}

	rec := do(t, h, "GET", "/v1/blocks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list blocks = %d, want 200", rec.Code)
	}
	var list struct {
		Items []BlockedUser `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].Handle != "author_two" {
		t.Fatalf("block list = %+v, want one entry for author_two", list.Items)
	}

	if rec := do(t, h, "DELETE", "/v1/blocks/"+r.other.String(), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock = %d, want 204", rec.Code)
	}
	rec = do(t, h, "GET", "/v1/blocks", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("block list after unblock = %+v, want empty", list.Items)
	}
}

// TestBlockIsIdempotent: tapping twice must not error, because the client
// cannot know whether the first tap landed.
func TestBlockIsIdempotent(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.viewer, true)
	for i := range 2 {
		if rec := do(t, h, "PUT", "/v1/blocks/"+r.other.String(), ""); rec.Code != http.StatusNoContent {
			t.Fatalf("block #%d = %d, want 204", i+1, rec.Code)
		}
	}
}

func TestBlockRejectsSelfAndAnonymous(t *testing.T) {
	r := newRig(t)

	rec := do(t, r.handler(r.viewer, true), "PUT", "/v1/blocks/"+r.viewer.String(), "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("self-block = %d, want 422", rec.Code)
	}

	rec = do(t, r.handler(uuid.UUID{}, false), "PUT", "/v1/blocks/"+r.other.String(), "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous block = %d, want 401", rec.Code)
	}

	rec = do(t, r.handler(r.viewer, true), "PUT", "/v1/blocks/not-a-uuid", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed id = %d, want 400", rec.Code)
	}
}

func TestReportRoundTrip(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.viewer, true)

	body := fmt.Sprintf(`{"target_type":"recipe","target_id":%q,"reason":"spam","details":"реклама"}`, r.recipe)
	rec := do(t, h, "POST", "/v1/reports", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("report = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}

	// The partial unique index allows one open report per reporter per target.
	rec = do(t, h, "POST", "/v1/reports", body)
	if rec.Code != http.StatusConflict {
		t.Errorf("duplicate report = %d, want 409", rec.Code)
	}
}

// TestReportRejectsUnknownTarget is the regression test for a hole the schema
// cannot close: reports.target_id is polymorphic and therefore carries no
// foreign key, so a complaint about a UUID that was never issued used to be
// inserted straight into the moderation queue.
func TestReportRejectsUnknownTarget(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.viewer, true)

	body := fmt.Sprintf(`{"target_type":"recipe","target_id":%q,"reason":"spam"}`, uuid.New())
	if rec := do(t, h, "POST", "/v1/reports", body); rec.Code != http.StatusNotFound {
		t.Errorf("report on unknown recipe = %d, want 404", rec.Code)
	}

	var queued int
	if err := r.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM reports`).Scan(&queued); err != nil {
		t.Fatalf("count reports: %v", err)
	}
	if queued != 0 {
		t.Errorf("moderation queue holds %d junk reports, want 0", queued)
	}
}

func TestReportValidation(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.viewer, true)

	for name, body := range map[string]string{
		"bad target type": `{"target_type":"planet","target_id":"` + r.recipe.String() + `","reason":"spam"}`,
		"bad reason":      `{"target_type":"recipe","target_id":"` + r.recipe.String() + `","reason":"вредный"}`,
		"bad id":          `{"target_type":"recipe","target_id":"nope","reason":"spam"}`,
		"self report":     `{"target_type":"user","target_id":"` + r.viewer.String() + `","reason":"spam"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := do(t, h, "POST", "/v1/reports", body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("= %d, want 422; body: %s", rec.Code, rec.Body.String())
			}
			// Messages are user-facing and must be in Russian, as everywhere else.
			if !strings.Contains(rec.Body.String(), `"fields"`) {
				t.Errorf("422 carries no per-field messages: %s", rec.Body.String())
			}
		})
	}
}
