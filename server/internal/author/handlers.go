package author

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
)

// ViewerFunc extracts the authenticated user id, or "" for anonymous callers,
// matching the convention internal/storefront established.
type ViewerFunc func(*http.Request) string

// API serves the public author pages.
type API struct {
	repo   *Repo
	search *search.Repo
	viewer ViewerFunc
}

func NewAPI(repo *Repo, s *search.Repo, viewer ViewerFunc) *API {
	if viewer == nil {
		viewer = func(*http.Request) string { return "" }
	}
	return &API{repo: repo, search: s, viewer: viewer}
}

func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/authors/{handle}", a.handleProfile)
	mux.HandleFunc("GET /v1/authors/{handle}/recipes", a.handleRecipes)
	mux.HandleFunc("PUT /v1/authors/{handle}/follow", a.handleFollow)
	mux.HandleFunc("DELETE /v1/authors/{handle}/follow", a.handleUnfollow)
}

func (a *API) handleProfile(w http.ResponseWriter, r *http.Request) {
	viewer := a.viewer(r)
	id, ok := a.resolve(w, r)
	if !ok {
		return
	}

	profile, err := a.repo.Profile(r.Context(), id, viewer)
	if err != nil {
		a.respondErr(w, r, err)
		return
	}
	// The body carries this viewer's follow state, so it must never land in a
	// shared cache. The storefront solves the same problem by caching only the
	// anonymous front page; here the response is per-viewer either way.
	if viewer != "" {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	httpx.JSONWithETag(w, r, http.StatusOK, profile)
}

// handleRecipes is the author's shelf. It reuses the storefront query engine
// wholesale, so the filters, sorts, cursors and block rules on an author page
// are the same code as on the front page and cannot drift from it.
func (a *API) handleRecipes(w http.ResponseWriter, r *http.Request) {
	viewer := a.viewer(r)
	id, ok := a.resolve(w, r)
	if !ok {
		return
	}

	q, err := search.ParseQuery(r.URL.Query())
	if err != nil {
		httpx.Respond(w, r, err)
		return
	}
	// The path decides the author, not the query string: ?author= on this route
	// would otherwise let a caller page someone else's shelf under this URL.
	q.AuthorID = id
	q.AuthorHandle = ""

	page, err := a.search.List(r.Context(), q, viewer)
	if err != nil {
		httpx.Respond(w, r, err)
		return
	}
	if viewer != "" {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	httpx.JSONWithETag(w, r, http.StatusOK, page)
}

func (a *API) handleFollow(w http.ResponseWriter, r *http.Request) {
	viewer := a.viewer(r)
	if viewer == "" {
		httpx.Respond(w, r, httpx.Unauthorized("Нужен вход в аккаунт"))
		return
	}
	id, ok := a.resolve(w, r)
	if !ok {
		return
	}
	switch err := a.repo.Follow(r.Context(), viewer, id); {
	case errors.Is(err, ErrSelf):
		httpx.Respond(w, r, httpx.Validation(map[string]string{
			"handle": "Нельзя подписаться на себя",
		}))
	case err != nil:
		a.respondErr(w, r, err)
	default:
		httpx.NoContent(w)
	}
}

func (a *API) handleUnfollow(w http.ResponseWriter, r *http.Request) {
	viewer := a.viewer(r)
	if viewer == "" {
		httpx.Respond(w, r, httpx.Unauthorized("Нужен вход в аккаунт"))
		return
	}
	id, ok := a.resolve(w, r)
	if !ok {
		return
	}
	if err := a.repo.Unfollow(r.Context(), viewer, id); err != nil {
		a.respondErr(w, r, err)
		return
	}
	httpx.NoContent(w)
}

// resolve turns the {handle} path segment into a user id, writing the response
// itself when it cannot.
func (a *API) resolve(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := r.PathValue("handle")
	if raw == "" || len(raw) > 64 {
		httpx.Respond(w, r, httpx.NotFound("Автор не найден"))
		return "", false
	}
	if _, err := uuid.Parse(raw); err != nil && !isHandle(strings.ToLower(raw)) {
		// Neither a handle nor an id: nothing to look up, and no reason to ask
		// the database.
		httpx.Respond(w, r, httpx.NotFound("Автор не найден"))
		return "", false
	}
	id, err := a.repo.Resolve(r.Context(), raw)
	if err != nil {
		a.respondErr(w, r, err)
		return "", false
	}
	return id, true
}

func (a *API) respondErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		httpx.Respond(w, r, httpx.NotFound("Автор не найден"))
		return
	}
	httpx.Respond(w, r, err)
}

// isHandle mirrors the users_handle_format CHECK in migration 0001.
func isHandle(s string) bool {
	if len(s) < 3 || len(s) > 30 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
	}
	return true
}
