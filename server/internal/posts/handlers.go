package posts

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/paging"
)

// ViewerFunc extracts the authenticated user id, or "" for anonymous callers.
type ViewerFunc func(*http.Request) string

// API serves the public blog and shorts endpoints.
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
	mux.HandleFunc("GET /v1/posts/{id}", a.handleGet)
	mux.HandleFunc("GET /v1/shorts", a.handleShorts)
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	viewer := a.viewer(r)
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Respond(w, r, httpx.NotFound("Публикация не найдена"))
		return
	}

	detail, err := a.repo.Get(r.Context(), id, viewer)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.Respond(w, r, httpx.NotFound("Публикация не найдена"))
			return
		}
		httpx.Respond(w, r, err)
		return
	}
	if viewer != "" {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	httpx.JSONWithETag(w, r, http.StatusOK, detail)
}

// handleShorts is the global vertical feed.
func (a *API) handleShorts(w http.ResponseWriter, r *http.Request) {
	viewer := a.viewer(r)
	opts, err := parseFeed(r, KindShort)
	if err != nil {
		httpx.Respond(w, r, err)
		return
	}
	// A short with no playable video is a dead card; the partial indexes
	// exclude them and this predicate has to match.
	opts.RequireVideoReady = true

	page, err := a.repo.List(r.Context(), opts, viewer)
	if err != nil {
		httpx.Respond(w, r, err)
		return
	}
	if viewer != "" {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	httpx.JSONWithETag(w, r, http.StatusOK, page)
}

// ParseFeed reads listing parameters, returning httpx.Validation with per-field
// Russian messages, matching how internal/search parses the storefront query.
func parseFeed(r *http.Request, kind string) (ListOptions, error) {
	v := r.URL.Query()
	o := ListOptions{Kind: kind, Sort: SortNew, Limit: DefaultLimit}
	problems := map[string]string{}

	if raw := strings.TrimSpace(v.Get("sort")); raw != "" {
		switch raw {
		case SortNew, SortTop:
			o.Sort = raw
		default:
			problems["sort"] = "Допустимые значения: new, top"
		}
	}

	if raw := v.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			problems["limit"] = "Ожидается число больше нуля"
		} else if n > MaxLimit {
			// Clamp rather than reject, as internal/search does.
			o.Limit = MaxLimit
		} else {
			o.Limit = n
		}
	}

	if raw := v.Get("cursor"); raw != "" {
		c, err := paging.DecodeFor(raw, SortNew, SortTop)
		if err != nil {
			problems["cursor"] = "Некорректный курсор постраничной навигации"
		} else if c.Sort != o.Sort {
			// Continuing a cursor under a different ordering would silently
			// skip or repeat rows.
			problems["cursor"] = "Курсор относится к другой сортировке"
		} else {
			o.Cursor = c
		}
	}

	if len(problems) > 0 {
		return ListOptions{}, httpx.Validation(problems)
	}
	return o, nil
}

// ParseFeedFor exposes listing-parameter parsing to packages that serve an
// author-scoped list of the same shape.
func ParseFeedFor(r *http.Request, kind string) (ListOptions, error) {
	return parseFeed(r, kind)
}
