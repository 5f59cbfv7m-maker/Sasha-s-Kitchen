package storefront

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/bundle"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/cache"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
)

// ViewerFunc extracts the authenticated user id, or "" for anonymous callers.
// It is injected so this package does not depend on the auth package.
type ViewerFunc func(*http.Request) string

// API serves the public storefront.
type API struct {
	pool    *pgxpool.Pool
	search  *search.Repo
	store   *Repo
	bundles *bundle.Repo
	cache   cache.Cache
	viewer  ViewerFunc
	feedTTL time.Duration
}

func NewAPI(pool *pgxpool.Pool, s *search.Repo, store *Repo, bundles *bundle.Repo,
	c cache.Cache, viewer ViewerFunc, feedTTL time.Duration) *API {
	if viewer == nil {
		viewer = func(*http.Request) string { return "" }
	}
	return &API{pool: pool, search: s, store: store, bundles: bundles,
		cache: c, viewer: viewer, feedTTL: feedTTL}
}

// Routes registers the public endpoints.
func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/storefront", a.handleFront)
	mux.HandleFunc("GET /v1/taxonomy", a.handleTaxonomy)
	mux.HandleFunc("GET /v1/recipes", a.handleSearch)
	mux.HandleFunc("GET /v1/recipes/facets", a.handleFacets)
	mux.HandleFunc("GET /v1/recipes/{id}", a.handleDetail)
	mux.HandleFunc("GET /v1/recipes/{id}/bundle", a.handleBundle)
}

func (a *API) handleFront(w http.ResponseWriter, r *http.Request) {
	viewer := a.viewer(r)
	perShelf := clampInt(r.URL.Query().Get("per_shelf"), 10, 1, 20)

	// Only the anonymous front page is cached. A signed-in viewer's page is
	// filtered by their blocks, so caching it under a shared key would leak
	// one person's moderation choices to everyone.
	key := "front:v1:" + strconv.Itoa(perShelf)
	if viewer == "" {
		var cached Front
		if a.cache.GetJSON(r.Context(), key, &cached) {
			httpx.JSONWithETag(w, r, http.StatusOK, cached)
			return
		}
	}

	front, err := a.store.Front(r.Context(), viewer, perShelf)
	if err != nil {
		httpx.Respond(w, r, httpx.Internal("Не удалось загрузить витрину").WithCause(err))
		return
	}
	if viewer == "" {
		a.cache.SetJSON(r.Context(), key, front, a.feedTTL)
	}
	httpx.JSONWithETag(w, r, http.StatusOK, front)
}

func (a *API) handleSearch(w http.ResponseWriter, r *http.Request) {
	q, err := search.ParseQuery(r.URL.Query())
	if err != nil {
		httpx.Respond(w, r, err)
		return
	}
	page, err := a.search.List(r.Context(), q, a.viewer(r))
	if err != nil {
		httpx.Respond(w, r, httpx.Internal("Не удалось выполнить поиск").WithCause(err))
		return
	}
	httpx.JSONWithETag(w, r, http.StatusOK, page)
}

func (a *API) handleFacets(w http.ResponseWriter, r *http.Request) {
	q, err := search.ParseQuery(r.URL.Query())
	if err != nil {
		httpx.Respond(w, r, err)
		return
	}
	facets, err := a.search.Facets(r.Context(), q, a.viewer(r))
	if err != nil {
		httpx.Respond(w, r, httpx.Internal("Не удалось посчитать фильтры").WithCause(err))
		return
	}
	httpx.JSONWithETag(w, r, http.StatusOK, facets)
}

func (a *API) handleDetail(w http.ResponseWriter, r *http.Request) {
	detail, err := a.store.Detail(r.Context(), r.PathValue("id"), a.viewer(r))
	if errors.Is(err, ErrNotFound) {
		httpx.Respond(w, r, httpx.NotFound("Рецепт не найден"))
		return
	}
	if err != nil {
		httpx.Respond(w, r, httpx.Internal("Не удалось загрузить рецепт").WithCause(err))
		return
	}
	httpx.JSONWithETag(w, r, http.StatusOK, detail)
}

// handleBundle returns the importable payload. It also records the import,
// which is the store's primary success metric.
func (a *API) handleBundle(w http.ResponseWriter, r *http.Request) {
	viewer := a.viewer(r)
	id := r.PathValue("id")

	b, err := a.bundles.Build(r.Context(), id, viewer)
	if errors.Is(err, bundle.ErrNotFound) {
		httpx.Respond(w, r, httpx.NotFound("Рецепт недоступен для импорта"))
		return
	}
	if err != nil {
		httpx.Respond(w, r, httpx.Internal("Не удалось собрать рецепт").WithCause(err))
		return
	}

	// Best effort: a failed metric write must not deny the user their recipe.
	if _, err := a.pool.Exec(r.Context(), `
		INSERT INTO recipe_imports (user_id, recipe_id, client)
		VALUES (nullif($1,'')::uuid, $2::uuid, $3)`,
		viewer, b.Recipe.SourceID, clip(r.UserAgent(), 200)); err != nil {
		httpx.LogWarn(r, "recipe import not recorded", err)
	}
	httpx.JSON(w, http.StatusOK, b)
}

// Taxonomy is the filter vocabulary the client renders its filter sheet from,
// so adding a cuisine never requires a client release.
type Taxonomy struct {
	Categories []Term `json:"categories"`
	Cuisines   []Term `json:"cuisines"`
	Diets      []Term `json:"diets"`
}

// Term is one filter value.
type Term struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

func (a *API) handleTaxonomy(w http.ResponseWriter, r *http.Request) {
	const key = "taxonomy:v1"
	var cached Taxonomy
	if a.cache.GetJSON(r.Context(), key, &cached) {
		httpx.JSONWithETag(w, r, http.StatusOK, cached)
		return
	}

	out := Taxonomy{Categories: []Term{}, Cuisines: []Term{}, Diets: []Term{}}
	for _, spec := range []struct {
		table string
		dst   *[]Term
	}{
		{"categories", &out.Categories},
		{"cuisines", &out.Cuisines},
		{"diet_tags", &out.Diets},
	} {
		// Table names come from this fixed literal list, never from input.
		rows, err := a.pool.Query(r.Context(),
			"SELECT slug, title FROM "+spec.table+" ORDER BY position ASC, title ASC")
		if err != nil {
			httpx.Respond(w, r, httpx.Internal("Не удалось загрузить справочники").WithCause(err))
			return
		}
		for rows.Next() {
			var t Term
			if err := rows.Scan(&t.Slug, &t.Title); err != nil {
				rows.Close()
				httpx.Respond(w, r, httpx.Internal("Не удалось прочитать справочник").WithCause(err))
				return
			}
			*spec.dst = append(*spec.dst, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			httpx.Respond(w, r, httpx.Internal("Не удалось прочитать справочник").WithCause(err))
			return
		}
	}

	// Taxonomy changes by hand, perhaps monthly; an hour of staleness is fine.
	a.cache.SetJSON(r.Context(), key, out, time.Hour)
	httpx.JSONWithETag(w, r, http.StatusOK, out)
}

func clampInt(raw string, def, min, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
