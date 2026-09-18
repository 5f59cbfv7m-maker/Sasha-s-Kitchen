// Package admin is the moderator's surface.
//
// internal/moderation has carried the publish workflow, the report queue and
// the takedown tools since the first schema, but registered no routes and was
// never mounted, so none of it could be reached. A moderation implementation
// nobody can call is the same as no moderation at all, which for an app with
// user-generated content is a review rejection waiting to happen.
package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/moderation"
)

const maxRequestBody = 8 << 10

// AdminFunc reports the caller's id and whether they are a moderator. It is
// injected rather than read from a context key this package defines, so that
// wiring it up is a compile-time obligation instead of a convention.
type AdminFunc func(*http.Request) (uuid.UUID, bool)

// API serves the moderator endpoints.
type API struct {
	mod   *moderation.Repo
	admin AdminFunc
}

func NewAPI(mod *moderation.Repo, admin AdminFunc) *API {
	if admin == nil {
		// Refusing everyone is the safe failure for a surface that can take
		// content down and suspend accounts.
		admin = func(*http.Request) (uuid.UUID, bool) { return uuid.UUID{}, false }
	}
	return &API{mod: mod, admin: admin}
}

func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/admin/queue", a.handleQueue)
	mux.HandleFunc("GET /v1/admin/reports", a.handleReports)
	mux.HandleFunc("POST /v1/admin/reports/{id}/resolve", a.handleResolveReport)

	mux.HandleFunc("POST /v1/admin/content/{kind}/{id}/approve", a.handleApprove)
	mux.HandleFunc("POST /v1/admin/content/{kind}/{id}/reject", a.handleReject)
	mux.HandleFunc("POST /v1/admin/content/{kind}/{id}/takedown", a.handleTakedown)

	mux.HandleFunc("POST /v1/admin/users/{id}/suspend", a.handleSuspend)
	mux.HandleFunc("POST /v1/admin/users/{id}/verify", a.handleVerify)
	mux.HandleFunc("DELETE /v1/admin/users/{id}/verify", a.handleUnverify)
}

// moderator resolves the caller, writing the response itself when they are not
// one. 404 rather than 403: the existence of this surface is not something an
// ordinary user needs confirmed.
func (a *API) moderator(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := a.admin(r)
	if !ok {
		httpx.Respond(w, r, httpx.NotFound("Не найдено"))
		return "", false
	}
	return id.String(), true
}

func (a *API) handleQueue(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.moderator(w, r); !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := a.mod.Pending(r.Context(), limit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) handleReports(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.moderator(w, r); !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := a.mod.OpenReports(r.Context(), limit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}

type resolveRequest struct {
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

func (a *API) handleResolveReport(w http.ResponseWriter, r *http.Request) {
	moderatorID, ok := a.moderator(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req resolveRequest
	if err := httpx.DecodeJSON(w, r, &req, maxRequestBody); err != nil {
		httpx.Respond(w, r, err)
		return
	}
	if req.Status != "upheld" && req.Status != "dismissed" {
		httpx.Respond(w, r, httpx.Validation(map[string]string{
			"status": "Допустимые значения: upheld, dismissed",
		}))
		return
	}
	if err := a.mod.ResolveReport(r.Context(), id, moderatorID, req.Status, req.Note); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

type reasonRequest struct {
	Reason string `json:"reason,omitempty"`
}

func (a *API) handleApprove(w http.ResponseWriter, r *http.Request) {
	moderatorID, kind, id, ok := a.contentTarget(w, r)
	if !ok {
		return
	}
	if err := a.mod.ApproveContent(r.Context(), kind, id, moderatorID); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (a *API) handleReject(w http.ResponseWriter, r *http.Request) {
	moderatorID, kind, id, ok := a.contentTarget(w, r)
	if !ok {
		return
	}
	reason, ok := a.reason(w, r, true)
	if !ok {
		return
	}
	if err := a.mod.RejectContent(r.Context(), kind, id, moderatorID, reason); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (a *API) handleTakedown(w http.ResponseWriter, r *http.Request) {
	moderatorID, kind, id, ok := a.contentTarget(w, r)
	if !ok {
		return
	}
	reason, ok := a.reason(w, r, true)
	if !ok {
		return
	}
	if err := a.mod.TakedownContent(r.Context(), kind, id, moderatorID, reason); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (a *API) handleSuspend(w http.ResponseWriter, r *http.Request) {
	moderatorID, ok := a.moderator(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	reason, ok := a.reason(w, r, true)
	if !ok {
		return
	}
	if err := a.mod.Suspend(r.Context(), id, moderatorID, reason); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (a *API) handleVerify(w http.ResponseWriter, r *http.Request)   { a.setVerified(w, r, true) }
func (a *API) handleUnverify(w http.ResponseWriter, r *http.Request) { a.setVerified(w, r, false) }

func (a *API) setVerified(w http.ResponseWriter, r *http.Request, verified bool) {
	moderatorID, ok := a.moderator(w, r)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := a.mod.SetVerified(r.Context(), id, moderatorID, verified); err != nil {
		a.fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

// contentTarget resolves moderator, content kind and id in one step, since
// every content action needs all three.
func (a *API) contentTarget(w http.ResponseWriter, r *http.Request) (moderatorID, kind, id string, ok bool) {
	moderatorID, ok = a.moderator(w, r)
	if !ok {
		return "", "", "", false
	}
	kind = r.PathValue("kind")
	if kind != moderation.KindRecipe && kind != moderation.KindPost {
		httpx.Respond(w, r, httpx.NotFound("Не найдено"))
		return "", "", "", false
	}
	id, ok = pathUUID(w, r, "id")
	if !ok {
		return "", "", "", false
	}
	return moderatorID, kind, id, true
}

// reason reads the explanation a moderator owes the author. Rejecting or
// taking something down without saying why leaves the author with nothing to
// act on, so it is required rather than optional.
func (a *API) reason(w http.ResponseWriter, r *http.Request, required bool) (string, bool) {
	var req reasonRequest
	if err := httpx.DecodeJSON(w, r, &req, maxRequestBody); err != nil {
		httpx.Respond(w, r, err)
		return "", false
	}
	if required && req.Reason == "" {
		httpx.Respond(w, r, httpx.Validation(map[string]string{
			"reason": "Укажите причину: автор должен понимать, что исправить",
		}))
		return "", false
	}
	return req.Reason, true
}

func pathUUID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		httpx.Respond(w, r, httpx.NotFound("Не найдено"))
		return "", false
	}
	return id.String(), true
}

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, moderation.ErrNotFound):
		httpx.Respond(w, r, httpx.NotFound("Не найдено"))
	case errors.Is(err, moderation.ErrBadState):
		httpx.Respond(w, r, httpx.Conflict("Это действие сейчас недоступно для этой публикации"))
	default:
		httpx.Respond(w, r, err)
	}
}
