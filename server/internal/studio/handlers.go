package studio

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
)

// maxRequestBody bounds an authoring payload. A recipe with a hundred products
// and a long article both fit comfortably; a megabyte of blocks does not.
const maxRequestBody = 512 << 10

// CallerFunc resolves the authenticated author, or false when anonymous.
type CallerFunc func(*http.Request) (uuid.UUID, bool)

// API is the author's own workspace. Every route here writes or reads the
// caller's own content, so all of them require a signed-in caller.
type API struct {
	repo   *Repo
	caller CallerFunc
}

func NewAPI(repo *Repo, caller CallerFunc) *API {
	if caller == nil {
		caller = func(*http.Request) (uuid.UUID, bool) { return uuid.UUID{}, false }
	}
	return &API{repo: repo, caller: caller}
}

func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/me/stats", a.handleStats)
	mux.HandleFunc("PATCH /v1/me/profile", a.handleProfile)

	mux.HandleFunc("GET /v1/me/posts", a.handleListPosts)
	mux.HandleFunc("POST /v1/me/posts", a.handleCreatePost)
	mux.HandleFunc("PATCH /v1/me/posts/{id}", a.handleUpdatePost)
	mux.HandleFunc("DELETE /v1/me/posts/{id}", a.handleDeletePost)
	mux.HandleFunc("POST /v1/me/posts/{id}/submit", a.handleSubmitPost)

	mux.HandleFunc("GET /v1/me/recipes", a.handleListRecipes)
	mux.HandleFunc("POST /v1/me/recipes", a.handleCreateRecipe)
	mux.HandleFunc("PATCH /v1/me/recipes/{id}", a.handleUpdateRecipe)
	mux.HandleFunc("DELETE /v1/me/recipes/{id}", a.handleDeleteRecipe)
	mux.HandleFunc("POST /v1/me/recipes/{id}/submit", a.handleSubmitRecipe)
}

// author resolves the caller, writing the response itself when anonymous.
func (a *API) author(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := a.caller(r)
	if !ok {
		httpx.Respond(w, r, httpx.Unauthorized("Нужен вход в аккаунт"))
		return "", false
	}
	return id.String(), true
}

func pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Respond(w, r, httpx.NotFound("Не найдено"))
		return "", false
	}
	return id.String(), true
}

func (a *API) handleStats(w http.ResponseWriter, r *http.Request) {
	id, ok := a.author(w, r)
	if !ok {
		return
	}
	stats, err := a.repo.Stats(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// The dashboard is per-author by definition and must never be shared.
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.JSON(w, http.StatusOK, stats)
}

func (a *API) handleProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := a.author(w, r)
	if !ok {
		return
	}
	var edit ProfileEdit
	if err := httpx.DecodeJSON(w, r, &edit, maxRequestBody); err != nil {
		httpx.Respond(w, r, err)
		return
	}
	problems, err := a.repo.UpdateProfile(r.Context(), id, edit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if problems != nil {
		httpx.Respond(w, r, httpx.Validation(problems))
		return
	}
	httpx.NoContent(w)
}

// ---------------------------------------------------------------- posts --

func (a *API) handleListPosts(w http.ResponseWriter, r *http.Request) {
	id, ok := a.author(w, r)
	if !ok {
		return
	}
	items, err := a.repo.ListOwnPosts(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) handleCreatePost(w http.ResponseWriter, r *http.Request) {
	id, ok := a.author(w, r)
	if !ok {
		return
	}
	draft, ok := a.decodePost(w, r, id)
	if !ok {
		return
	}
	postID, err := a.repo.CreatePost(r.Context(), id, draft)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": postID})
}

func (a *API) handleUpdatePost(w http.ResponseWriter, r *http.Request) {
	id, ok := a.author(w, r)
	if !ok {
		return
	}
	postID, ok := pathID(w, r)
	if !ok {
		return
	}
	draft, ok := a.decodePost(w, r, id)
	if !ok {
		return
	}
	if err := a.repo.UpdatePost(r.Context(), id, postID, draft); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (a *API) decodePost(w http.ResponseWriter, r *http.Request, authorID string) (PostDraft, bool) {
	var draft PostDraft
	if err := httpx.DecodeJSON(w, r, &draft, maxRequestBody); err != nil {
		httpx.Respond(w, r, err)
		return PostDraft{}, false
	}
	problems, err := a.repo.ValidateDraft(r.Context(), authorID, &draft)
	if err != nil {
		a.fail(w, r, err)
		return PostDraft{}, false
	}
	if problems != nil {
		httpx.Respond(w, r, httpx.Validation(problems))
		return PostDraft{}, false
	}
	return draft, true
}

func (a *API) handleDeletePost(w http.ResponseWriter, r *http.Request) {
	id, ok := a.author(w, r)
	if !ok {
		return
	}
	postID, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.repo.DeletePost(r.Context(), id, postID); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (a *API) handleSubmitPost(w http.ResponseWriter, r *http.Request) {
	id, ok := a.author(w, r)
	if !ok {
		return
	}
	postID, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := a.repo.SubmitPost(r.Context(), id, postID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// -------------------------------------------------------------- recipes --

func (a *API) handleListRecipes(w http.ResponseWriter, r *http.Request) {
	id, ok := a.author(w, r)
	if !ok {
		return
	}
	items, err := a.repo.ListOwnRecipes(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) handleCreateRecipe(w http.ResponseWriter, r *http.Request) {
	id, ok := a.author(w, r)
	if !ok {
		return
	}
	draft, ok := a.decodeRecipe(w, r, id)
	if !ok {
		return
	}
	recipeID, err := a.repo.CreateRecipe(r.Context(), id, draft)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": recipeID})
}

func (a *API) handleUpdateRecipe(w http.ResponseWriter, r *http.Request) {
	id, ok := a.author(w, r)
	if !ok {
		return
	}
	recipeID, ok := pathID(w, r)
	if !ok {
		return
	}
	draft, ok := a.decodeRecipe(w, r, id)
	if !ok {
		return
	}
	if err := a.repo.UpdateRecipe(r.Context(), id, recipeID, draft); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (a *API) decodeRecipe(w http.ResponseWriter, r *http.Request, authorID string) (RecipeDraft, bool) {
	var draft RecipeDraft
	if err := httpx.DecodeJSON(w, r, &draft, maxRequestBody); err != nil {
		httpx.Respond(w, r, err)
		return RecipeDraft{}, false
	}
	problems, err := a.repo.ValidateRecipe(r.Context(), authorID, &draft)
	if err != nil {
		a.fail(w, r, err)
		return RecipeDraft{}, false
	}
	if problems != nil {
		httpx.Respond(w, r, httpx.Validation(problems))
		return RecipeDraft{}, false
	}
	return draft, true
}

func (a *API) handleDeleteRecipe(w http.ResponseWriter, r *http.Request) {
	id, ok := a.author(w, r)
	if !ok {
		return
	}
	recipeID, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.repo.DeleteRecipe(r.Context(), id, recipeID); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (a *API) handleSubmitRecipe(w http.ResponseWriter, r *http.Request) {
	id, ok := a.author(w, r)
	if !ok {
		return
	}
	recipeID, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := a.repo.SubmitRecipe(r.Context(), id, recipeID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// fail maps the package's sentinel errors onto HTTP, keeping the mapping in one
// place rather than repeating errors.Is chains in every handler.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Respond(w, r, httpx.NotFound("Не найдено"))
	case errors.Is(err, ErrQuota):
		httpx.Respond(w, r, httpx.RateLimited(
			"Достигнут дневной лимит публикаций. Он вырастет, когда модератор одобрит ваши первые работы"))
	case errors.Is(err, ErrBadState):
		httpx.Respond(w, r, httpx.Conflict("Эту публикацию сейчас нельзя отправить"))
	case errors.Is(err, ErrNoIngredients):
		httpx.Respond(w, r, httpx.Validation(map[string]string{
			"ingredients": "Добавьте продукты: без них рецепт нельзя забрать себе",
		}))
	case errors.Is(err, ErrDuplicateSlug):
		httpx.Respond(w, r, httpx.Conflict("У вас уже есть публикация с таким адресом"))
	case errors.Is(err, ErrHandleTaken):
		httpx.Respond(w, r, httpx.Conflict("Этот псевдоним уже занят"))
	default:
		httpx.Respond(w, r, err)
	}
}
