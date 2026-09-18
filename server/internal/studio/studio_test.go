package studio

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
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/posts"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/testdb"
)

type stubResolver struct{}

func (stubResolver) PublicURL(key string) string { return "https://cdn.test/" + key }

type rig struct {
	pool  *pgxpool.Pool
	repo  *Repo
	anna  uuid.UUID
	boris uuid.UUID
	media string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testdb.Pool(t, "studio")
	testdb.Truncate(t, pool, "users", "recipes", "posts", "media_assets",
		"moderation_events", "user_stats", "user_handle_history")

	ctx := context.Background()
	searchRepo := search.NewRepo(pool, stubResolver{})
	r := &rig{pool: pool,
		repo: NewRepo(pool, posts.NewRepo(pool, searchRepo, stubResolver{}), moderation.NewRepo(pool))}

	mk := func(handle string, trust int) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (handle, display_name, is_author, trust_level)
			VALUES ($1, $2, true, $3) RETURNING id`, handle, handle, int16(trust)).Scan(&id); err != nil {
			t.Fatalf("create user: %v", err)
		}
		return id
	}
	r.anna = mk("chef_anna", 0)   // newcomer: premoderated, 3 a day
	r.boris = mk("chef_boris", 1) // trusted: publishes straight away

	if err := pool.QueryRow(ctx, `
		INSERT INTO media_assets (owner_id, kind, status, storage_key, poster_key, hls_key)
		VALUES ($1, 'video', 'ready', 'orig/v.mp4', 'poster/v.jpg', 'hls/v.m3u8')
		RETURNING id::text`, r.anna).Scan(&r.media); err != nil {
		t.Fatalf("create media: %v", err)
	}
	return r
}

func (r *rig) handler(caller uuid.UUID, authed bool) http.Handler {
	mux := http.NewServeMux()
	NewAPI(r.repo, func(*http.Request) (uuid.UUID, bool) { return caller, authed }).Routes(mux)
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

func recipeBody(title string) string {
	return fmt.Sprintf(`{
	  "title": %q, "difficulty": 1, "cook_time_minutes": 30, "base_servings": 2,
	  "steps": [{"text":"Смешать"},{"text":"Запечь"}],
	  "ingredients": [
	    {"product_name":"Мука","amount_per_base_serving":200,"unit":"г","grams_per_unit":1,"kcal_per_100":364},
	    {"product_name":"Яйцо","amount_per_base_serving":2,"unit":"шт","grams_per_unit":55,"kcal_per_100":157}
	  ]}`, title)
}

func TestRecipeLifecycle(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.anna, true)

	rec := do(t, h, "POST", "/v1/me/recipes", recipeBody("Борщ по-домашнему"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var created struct{ ID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// A draft is not on the storefront.
	var status string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT status FROM recipes WHERE id=$1::uuid`, created.ID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "draft" {
		t.Errorf("new recipe status = %q, want draft", status)
	}

	// The slug is transliterated from a Russian title rather than left empty.
	var slug string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT slug FROM recipes WHERE id=$1::uuid`, created.ID).Scan(&slug); err != nil {
		t.Fatalf("read slug: %v", err)
	}
	if slug != "borsch-po-domashnemu" {
		t.Errorf("slug = %q, want borsch-po-domashnemu", slug)
	}

	// Derived nutrition is computed by the trigger from the ingredients.
	var kcal float64
	if err := r.pool.QueryRow(context.Background(),
		`SELECT kcal_per_serving FROM recipes WHERE id=$1::uuid`, created.ID).Scan(&kcal); err != nil {
		t.Fatalf("read kcal: %v", err)
	}
	// 200 г муки = 728 ккал, 2 яйца по 55 г = 172.7 ккал; на 2 порции ≈ 450.
	if kcal < 400 || kcal > 500 {
		t.Errorf("kcal per serving = %.1f, expected around 450", kcal)
	}

	// A newcomer is premoderated, so submitting queues rather than publishes.
	rec = do(t, h, "POST", "/v1/me/recipes/"+created.ID+"/submit", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("submit = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var out publishOutcome
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Status != "review" {
		t.Errorf("newcomer submit = %q, want review", out.Status)
	}
}

// TestTrustedAuthorPublishesImmediately is the other half of the trust ladder.
func TestTrustedAuthorPublishesImmediately(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.boris, true)

	rec := do(t, h, "POST", "/v1/me/recipes", recipeBody("Плов"))
	var created struct{ ID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rec = do(t, h, "POST", "/v1/me/recipes/"+created.ID+"/submit", "")
	var out publishOutcome
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Status != "published" || out.PublishedAt == nil {
		t.Errorf("trusted submit = %+v, want published with a timestamp", out)
	}
}

// TestDailyQuota is the load-bearing rule of "anyone can publish, with a
// limit": a newcomer gets three a day and the fourth is refused.
func TestDailyQuota(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.anna, true)

	for i := range 4 {
		rec := do(t, h, "POST", "/v1/me/recipes", recipeBody(fmt.Sprintf("Рецепт номер %d", i)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create #%d = %d; body: %s", i, rec.Code, rec.Body.String())
		}
		var created struct{ ID string }
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode: %v", err)
		}

		rec = do(t, h, "POST", "/v1/me/recipes/"+created.ID+"/submit", "")
		if i < 3 {
			if rec.Code != http.StatusOK {
				t.Fatalf("submit #%d = %d, want 200; body: %s", i+1, rec.Code, rec.Body.String())
			}
			continue
		}
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("submit #4 = %d, want 429 (daily quota is 3)", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "лимит") {
			t.Errorf("429 body does not explain the limit in Russian: %s", rec.Body.String())
		}
	}
}

// TestQuotaCountsTransitionsNotRows: a draft sent, rejected and sent again is
// one row and two publications. Counting rows would let an author cycle one
// draft past the limit forever.
func TestQuotaCountsTransitionsNotRows(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.anna, true)
	ctx := context.Background()

	rec := do(t, h, "POST", "/v1/me/recipes", recipeBody("Единственный рецепт"))
	var created struct{ ID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for i := range 3 {
		if rec := do(t, h, "POST", "/v1/me/recipes/"+created.ID+"/submit", ""); rec.Code != http.StatusOK {
			t.Fatalf("submit #%d = %d; body: %s", i+1, rec.Code, rec.Body.String())
		}
		// A moderator sends it back, so the author can submit it again.
		if _, err := r.pool.Exec(ctx,
			`UPDATE recipes SET status='rejected' WHERE id=$1::uuid`, created.ID); err != nil {
			t.Fatalf("reject: %v", err)
		}
	}
	if rec := do(t, h, "POST", "/v1/me/recipes/"+created.ID+"/submit", ""); rec.Code != http.StatusTooManyRequests {
		t.Errorf("4th submission of the same row = %d, want 429", rec.Code)
	}
}

func TestCannotTouchSomeoneElsesContent(t *testing.T) {
	r := newRig(t)

	rec := do(t, r.handler(r.anna, true), "POST", "/v1/me/recipes", recipeBody("Анин рецепт"))
	var created struct{ ID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	intruder := r.handler(r.boris, true)
	for _, tc := range []struct{ method, target, body string }{
		{"PATCH", "/v1/me/recipes/" + created.ID, recipeBody("Теперь мой")},
		{"DELETE", "/v1/me/recipes/" + created.ID, ""},
		{"POST", "/v1/me/recipes/" + created.ID + "/submit", ""},
	} {
		rec := do(t, intruder, tc.method, tc.target, tc.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s on another author's recipe = %d, want 404", tc.method, rec.Code)
		}
	}
}

func TestAnonymousIsRefusedEverywhere(t *testing.T) {
	r := newRig(t)
	h := r.handler(uuid.UUID{}, false)
	for _, tc := range []struct{ method, target string }{
		{"GET", "/v1/me/stats"}, {"PATCH", "/v1/me/profile"},
		{"GET", "/v1/me/posts"}, {"POST", "/v1/me/posts"},
		{"GET", "/v1/me/recipes"}, {"POST", "/v1/me/recipes"},
	} {
		if rec := do(t, h, tc.method, tc.target, "{}"); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", tc.method, tc.target, rec.Code)
		}
	}
}

func TestRecipeValidation(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.anna, true)

	for name, body := range map[string]string{
		"no ingredients": `{"title":"Пустой","difficulty":1,"base_servings":2,"ingredients":[]}`,
		"duplicate product": `{"title":"Дубль","difficulty":1,"base_servings":2,"ingredients":[
			{"product_name":"Мука","amount_per_base_serving":1,"unit":"г","grams_per_unit":1},
			{"product_name":"мука ","amount_per_base_serving":2,"unit":"г","grams_per_unit":1}]}`,
		"bad unit": `{"title":"Единицы","difficulty":1,"base_servings":2,"ingredients":[
			{"product_name":"Мука","amount_per_base_serving":1,"unit":"фунт","grams_per_unit":1}]}`,
		"paid not supported": `{"title":"Платный","difficulty":1,"base_servings":2,"access_tier":"paid",
			"price_minor":10000,"currency":"RUB","ingredients":[
			{"product_name":"Мука","amount_per_base_serving":1,"unit":"г","grams_per_unit":1}]}`,
		"bad difficulty": `{"title":"Сложность","difficulty":9,"base_servings":2,"ingredients":[
			{"product_name":"Мука","amount_per_base_serving":1,"unit":"г","grams_per_unit":1}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := do(t, h, "POST", "/v1/me/recipes", body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("= %d, want 422; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestSubmitRefusesRecipeWithoutIngredients: a recipe nobody can import is the
// one thing this store must not publish.
func TestSubmitRefusesRecipeWithoutIngredients(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	var id string
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO recipes (author_id, slug, title, status)
		VALUES ($1, 'empty', 'Без продуктов', 'draft') RETURNING id::text`, r.anna).Scan(&id); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec := do(t, r.handler(r.anna, true), "POST", "/v1/me/recipes/"+id+"/submit", "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("= %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

func TestPostLifecycleAndShortNeedsVideo(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.anna, true)

	article := fmt.Sprintf(`{"kind":"article","title":"Как я варю борщ",
		"blocks":[{"type":"paragraph","text":"Вступление"},
		          {"type":"photo","media_id":%q,"caption":"Кадр"}]}`, r.media)
	rec := do(t, h, "POST", "/v1/me/posts", article)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create article = %d; body: %s", rec.Code, rec.Body.String())
	}

	// A short without video is refused by validation, not by a constraint error.
	rec = do(t, h, "POST", "/v1/me/posts", `{"kind":"short","title":"Без видео","blocks":[]}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("short without video = %d, want 422", rec.Code)
	}

	short := fmt.Sprintf(`{"kind":"short","title":"Шортс про борщ","video_media_id":%q,"blocks":[]}`, r.media)
	rec = do(t, h, "POST", "/v1/me/posts", short)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create short = %d; body: %s", rec.Code, rec.Body.String())
	}
	var created struct{ ID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The media was already ready when the post was written, and the trigger on
	// media_assets only fires on a status change, so the flag has to be set now.
	var ready bool
	if err := r.pool.QueryRow(context.Background(),
		`SELECT video_ready FROM posts WHERE id=$1::uuid`, created.ID).Scan(&ready); err != nil {
		t.Fatalf("read video_ready: %v", err)
	}
	if !ready {
		t.Error("video_ready is false although the asset was already ready")
	}
}

// TestCannotEmbedSomeoneElsesMedia closes the hole that makes raw ids in a body
// dangerous: knowing an id must not be enough to publish someone's file.
func TestCannotEmbedSomeoneElsesMedia(t *testing.T) {
	r := newRig(t)
	body := fmt.Sprintf(`{"kind":"article","title":"Чужая фотография",
		"blocks":[{"type":"photo","media_id":%q}]}`, r.media)
	rec := do(t, r.handler(r.boris, true), "POST", "/v1/me/posts", body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("= %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

func TestProfileEditAndRename(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.anna, true)
	ctx := context.Background()

	rec := do(t, h, "PATCH", "/v1/me/profile",
		`{"display_name":"Анна Петрова","bio":"Готовлю дома","handle":"anna_cooks",
		  "links":[{"title":"Сайт","url":"https://example.com"}]}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("= %d, want 204; body: %s", rec.Code, rec.Body.String())
	}

	var handle, name string
	if err := r.pool.QueryRow(ctx,
		`SELECT handle::text, display_name FROM users WHERE id=$1`, r.anna).Scan(&handle, &name); err != nil {
		t.Fatalf("read: %v", err)
	}
	if handle != "anna_cooks" || name != "Анна Петрова" {
		t.Errorf("profile = %s/%s", handle, name)
	}

	// The old handle must keep resolving, or every shared link dies on rename.
	var recorded string
	if err := r.pool.QueryRow(ctx,
		`SELECT old_handle::text FROM user_handle_history WHERE user_id=$1`, r.anna).Scan(&recorded); err != nil {
		t.Fatalf("history: %v", err)
	}
	if recorded != "chef_anna" {
		t.Errorf("history recorded %q, want chef_anna", recorded)
	}

	// Taking someone else's live handle must fail.
	rec = do(t, h, "PATCH", "/v1/me/profile", `{"handle":"chef_boris"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("taking a live handle = %d, want 409", rec.Code)
	}
}

// TestProfileRejectsScriptLinks: a javascript: URL rendered as a profile link
// is a script-injection vector on whichever client opens it.
func TestProfileRejectsScriptLinks(t *testing.T) {
	r := newRig(t)
	rec := do(t, r.handler(r.anna, true), "PATCH", "/v1/me/profile",
		`{"links":[{"title":"Клик","url":"javascript:alert(1)"}]}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("= %d, want 422", rec.Code)
	}
}

// TestFeaturedMustBeOwnAndPublished stops an author pinning a draft, or
// someone else's work, to their public page.
func TestFeaturedMustBeOwnAndPublished(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()

	var draft string
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO recipes (author_id, slug, title, status)
		VALUES ($1, 'anna-draft', 'Черновик', 'draft') RETURNING id::text`, r.anna).Scan(&draft); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var borisLive string
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO recipes (author_id, slug, title, status, published_at)
		VALUES ($1, 'boris-live', 'Опубликован', 'published', now()) RETURNING id::text`,
		r.boris).Scan(&borisLive); err != nil {
		t.Fatalf("seed: %v", err)
	}

	h := r.handler(r.anna, true)
	for name, id := range map[string]string{"own draft": draft, "someone else's": borisLive} {
		t.Run(name, func(t *testing.T) {
			rec := do(t, h, "PATCH", "/v1/me/profile",
				fmt.Sprintf(`{"featured_recipe_id":%q}`, id))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("= %d, want 422; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestStatsReportsWorkflowState(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.anna, true)

	rec := do(t, h, "POST", "/v1/me/recipes", recipeBody("Рецепт для статистики"))
	var created struct{ ID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec := do(t, h, "POST", "/v1/me/recipes/"+created.ID+"/submit", ""); rec.Code != http.StatusOK {
		t.Fatalf("submit = %d", rec.Code)
	}

	rec = do(t, h, "GET", "/v1/me/stats", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200", rec.Code)
	}
	var s Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s.Recipes.Review != 1 || s.PendingModeration != 1 {
		t.Errorf("stats = %+v, want one recipe awaiting moderation", s)
	}
	if s.PublishedToday != 1 || s.DailyLimit != 3 {
		t.Errorf("quota reporting = %d/%d, want 1/3", s.PublishedToday, s.DailyLimit)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("dashboard Cache-Control = %q; it is per-author and must not be shared", cc)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Как варить борщ": "kak-varit-borsch",
		"  Ёлки  палки  ": "elki-palki",
		"Plain English":   "plain-english",
		"---":             "",
		"Щи да каша":      "schi-da-kasha",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
