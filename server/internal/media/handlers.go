package media

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/objstore"
)

// maxRequestBody caps the JSON bodies here; the media itself never passes
// through the API, only these small control messages.
const maxRequestBody = 4 << 10

// CallerFunc resolves the authenticated caller of a request, returning false
// for an anonymous one. It is injected rather than read from a context key
// this package defines, so that wiring it up is a compile-time obligation
// instead of a convention someone has to remember.
//
// An earlier version did define its own context key and expected whatever
// verified the credentials to populate it. Nothing ever did, so every upload
// answered 401 and no test noticed, because none of them went through the
// router. Hence this parameter.
type CallerFunc func(*http.Request) (uuid.UUID, bool)

// API exposes the upload endpoints.
type API struct {
	svc    *Service
	repo   *Repo
	store  objstore.Store
	caller CallerFunc
}

func NewAPI(svc *Service, repo *Repo, store objstore.Store, caller CallerFunc) *API {
	if caller == nil {
		// Refusing every request is the safe failure: an upload endpoint that
		// cannot identify its caller must not accept anonymous writes.
		caller = func(*http.Request) (uuid.UUID, bool) { return uuid.UUID{}, false }
	}
	return &API{svc: svc, repo: repo, store: store, caller: caller}
}

// Routes registers the media endpoints. Uploading requires an authenticated
// caller, resolved by the CallerFunc given to NewAPI.
func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/media/uploads", a.handleCreateUpload)
	mux.HandleFunc("POST /v1/media/{id}/complete", a.handleComplete)
	mux.HandleFunc("GET /v1/media/{id}", a.handleGet)
}

type createUploadRequest struct {
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	Bytes       int64  `json:"bytes"`
}

func (a *API) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	owner, ok := a.caller(r)
	if !ok {
		httpx.Respond(w, r, httpx.Unauthorized("Нужен вход в аккаунт"))
		return
	}
	var req createUploadRequest
	if err := httpx.DecodeJSON(w, r, &req, maxRequestBody); err != nil {
		httpx.Respond(w, r, err)
		return
	}

	ticket, err := a.svc.CreateUploadTicket(r.Context(), owner,
		Kind(req.Kind), req.ContentType, req.Bytes)
	switch {
	case errors.Is(err, ErrUnsupportedType):
		httpx.Respond(w, r, httpx.Validation(map[string]string{
			"content_type": "Такой тип файла не поддерживается"}))
		return
	case errors.Is(err, ErrTooLarge):
		httpx.Respond(w, r, httpx.Validation(map[string]string{
			"bytes": "Файл превышает допустимый размер"}))
		return
	case err != nil:
		httpx.Respond(w, r, httpx.Internal("Не удалось подготовить загрузку").WithCause(err))
		return
	}
	httpx.JSON(w, http.StatusCreated, ticket)
}

func (a *API) handleComplete(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.caller(r)
	if !ok {
		httpx.Respond(w, r, httpx.Unauthorized("Нужен вход в аккаунт"))
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Respond(w, r, httpx.BadRequest("Некорректный идентификатор файла"))
		return
	}

	asset, err := a.svc.CompleteUpload(r.Context(), id, caller)
	switch {
	case errors.Is(err, ErrForbidden):
		// Deliberately 404, not 403: confirming that someone else's asset
		// exists would leak the id space.
		httpx.Respond(w, r, httpx.NotFound("Файл не найден"))
		return
	case errors.Is(err, ErrNotUploaded):
		httpx.Respond(w, r, httpx.Conflict("Файл ещё не загружен в хранилище"))
		return
	case errors.Is(err, ErrTooLarge), errors.Is(err, ErrUnsupportedType):
		httpx.Respond(w, r, httpx.BadRequest("Загруженный файл не прошёл проверку"))
		return
	case err != nil:
		httpx.Respond(w, r, httpx.NotFound("Файл не найден"))
		return
	}
	httpx.JSON(w, http.StatusOK, a.view(asset))
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Respond(w, r, httpx.BadRequest("Некорректный идентификатор файла"))
		return
	}
	asset, err := a.repo.Get(r.Context(), id)
	if err != nil {
		httpx.Respond(w, r, httpx.NotFound("Файл не найден"))
		return
	}
	httpx.JSON(w, http.StatusOK, a.view(asset))
}

// assetView is the client-facing shape. Storage keys never appear in it: the
// client gets CDN URLs and nothing about the bucket layout.
type assetView struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	URL        string `json:"url,omitempty"`
	PosterURL  string `json:"poster_url,omitempty"`
	Blurhash   string `json:"blurhash,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMs int    `json:"duration_ms,omitempty"`
}

func (a *API) view(asset Asset) assetView {
	v := assetView{
		ID:     asset.ID.String(),
		Kind:   string(asset.Kind),
		Status: string(asset.Status),
	}
	if asset.Blurhash != nil {
		v.Blurhash = *asset.Blurhash
	}
	if asset.Width != nil {
		v.Width = *asset.Width
	}
	if asset.Height != nil {
		v.Height = *asset.Height
	}
	if asset.DurationMs != nil {
		v.DurationMs = *asset.DurationMs
	}
	// URLs are only meaningful once the derivatives exist.
	if asset.Status != StatusReady {
		return v
	}
	if asset.Kind == KindVideo {
		if asset.HLSKey != nil {
			v.URL = a.store.PublicURL(*asset.HLSKey)
		}
		if asset.PosterKey != nil {
			v.PosterURL = a.store.PublicURL(*asset.PosterKey)
		}
		return v
	}
	v.URL = a.store.PublicURL(PhotoDerivativeKey(asset.ID, "card"))
	return v
}
