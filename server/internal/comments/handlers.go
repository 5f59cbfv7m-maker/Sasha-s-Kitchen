package comments

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/paging"
)

const maxRequestBody = 8 << 10

// ViewerFunc extracts the authenticated user id, or "" for anonymous callers.
type ViewerFunc func(*http.Request) string

// API serves comment threads.
type API struct {
	repo   *Repo
	viewer ViewerFunc
}

func NewAPI(repo *Repo, viewer ViewerFunc) *API {
	if viewer == nil {
		viewer = func(*http.Request) string { return "" }
	}
	return &API{repo: repo, viewer: viewer}
}

func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/{kind}/{id}/comments", a.handleList)
	mux.HandleFunc("POST /v1/{kind}/{id}/comments", a.handleCreate)
	mux.HandleFunc("DELETE /v1/comments/{id}", a.handleDelete)
}

// target reads the {kind}/{id} pair, which is shared by the mounted routes.
//
// The kind comes from the path rather than a body field so the same URL shape
// works under /v1/recipes/... and /v1/posts/..., matching where a client is
// already looking.
func (a *API) target(w http.ResponseWriter, r *http.Request) (Target, bool) {
	var kind string
	switch r.PathValue("kind") {
	case "recipes":
		kind = "recipe"
	case "posts":
		kind = "post"
	default:
		httpx.Respond(w, r, httpx.NotFound("Не найдено"))
		return Target{}, false
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Respond(w, r, httpx.NotFound("Не найдено"))
		return Target{}, false
	}
	return Target{Kind: kind, ID: id.String()}, true
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	t, ok := a.target(w, r)
	if !ok {
		return
	}
	viewer := a.viewer(r)

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var cur *paging.Cursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		c, err := paging.DecodeFor(raw, SortNew)
		if err != nil {
			httpx.Respond(w, r, httpx.Validation(map[string]string{
				"cursor": "Некорректный курсор постраничной навигации",
			}))
			return
		}
		cur = c
	}

	page, err := a.repo.List(r.Context(), t, viewer, cur, limit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if viewer != "" {
		// The body marks the viewer's own comments, so it is per-viewer.
		w.Header().Set("Cache-Control", "private, no-store")
	}
	httpx.JSONWithETag(w, r, http.StatusOK, page)
}

type createRequest struct {
	Body     string `json:"body"`
	ParentID string `json:"parent_id,omitempty"`
}

func (a *API) handleCreate(w http.ResponseWriter, r *http.Request) {
	t, ok := a.target(w, r)
	if !ok {
		return
	}
	viewer := a.viewer(r)
	if viewer == "" {
		httpx.Respond(w, r, httpx.Unauthorized("Нужен вход в аккаунт"))
		return
	}

	var req createRequest
	if err := httpx.DecodeJSON(w, r, &req, maxRequestBody); err != nil {
		httpx.Respond(w, r, err)
		return
	}
	if req.ParentID != "" {
		if _, err := uuid.Parse(req.ParentID); err != nil {
			httpx.Respond(w, r, httpx.Validation(map[string]string{
				"parent_id": "Некорректный идентификатор",
			}))
			return
		}
	}

	id, err := a.repo.Create(r.Context(), t, viewer, req.ParentID, req.Body)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	viewer := a.viewer(r)
	if viewer == "" {
		httpx.Respond(w, r, httpx.Unauthorized("Нужен вход в аккаунт"))
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Respond(w, r, httpx.NotFound("Не найдено"))
		return
	}
	if err := a.repo.Delete(r.Context(), id.String(), viewer); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Respond(w, r, httpx.NotFound("Не найдено"))
	case errors.Is(err, ErrTooFast):
		httpx.Respond(w, r, httpx.RateLimited("Слишком часто. Подождите минуту"))
	case errors.Is(err, ErrBadParent):
		httpx.Respond(w, r, httpx.Validation(map[string]string{
			"parent_id": "Ответить можно только на комментарий верхнего уровня под этой же публикацией",
		}))
	default:
		// A body that failed the length check arrives here as a plain error.
		httpx.Respond(w, r, httpx.Validation(map[string]string{
			"body": "Комментарий от 1 до 2000 символов",
		}))
	}
}
