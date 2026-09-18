// Package social exposes the viewer-side safety actions: reporting content and
// blocking an author.
//
// The moderation repository has had these operations since the first schema,
// and the storefront has always filtered listings by user_blocks. What was
// missing was any way for a user to create a block: internal/moderation
// registered no routes and cmd/api never imported it. App Store Guideline 1.2
// requires a UGC app to offer reporting and blocking from the interface, so an
// unreachable implementation is the same as none.
package social

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/moderation"
)

// maxRequestBody caps the small JSON control messages this package accepts.
const maxRequestBody = 8 << 10

// CallerFunc resolves the authenticated caller, or false when anonymous. It is
// injected so this package does not depend on internal/auth.
type CallerFunc func(*http.Request) (uuid.UUID, bool)

// API serves the reporting and blocking endpoints.
type API struct {
	mod    *moderation.Repo
	caller CallerFunc
}

func NewAPI(mod *moderation.Repo, caller CallerFunc) *API {
	if caller == nil {
		caller = func(*http.Request) (uuid.UUID, bool) { return uuid.UUID{}, false }
	}
	return &API{mod: mod, caller: caller}
}

// Routes registers the endpoints. All of them require a signed-in caller:
// an anonymous report cannot be rate-limited per person and an anonymous block
// has nobody to apply to.
func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/reports", a.handleReport)
	mux.HandleFunc("GET /v1/blocks", a.handleListBlocks)
	mux.HandleFunc("PUT /v1/blocks/{id}", a.handleBlock)
	mux.HandleFunc("DELETE /v1/blocks/{id}", a.handleUnblock)
}

// reportReasons mirrors the reports.reason CHECK constraint in
// migrations/0003_storefront_social_moderation.sql.
var reportReasons = map[string]bool{
	"spam": true, "offensive": true, "copyright": true,
	"unsafe_food": true, "sexual": true, "other": true,
}

// reportTargets mirrors the reports.target_type CHECK constraint.
var reportTargets = map[string]bool{"recipe": true, "user": true, "rating": true}

const maxReportDetails = 2000

type reportRequest struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Reason     string `json:"reason"`
	Details    string `json:"details,omitempty"`
}

type reportResponse struct {
	ID string `json:"id"`
}

func (a *API) handleReport(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.caller(r)
	if !ok {
		httpx.Respond(w, r, httpx.Unauthorized("Нужен вход в аккаунт"))
		return
	}

	var req reportRequest
	if err := httpx.DecodeJSON(w, r, &req, maxRequestBody); err != nil {
		httpx.Respond(w, r, err)
		return
	}

	problems := map[string]string{}
	if !reportTargets[req.TargetType] {
		problems["target_type"] = "Допустимые значения: recipe, user, rating"
	}
	if _, err := uuid.Parse(req.TargetID); err != nil {
		problems["target_id"] = "Некорректный идентификатор"
	}
	if !reportReasons[req.Reason] {
		problems["reason"] = "Допустимые значения: spam, offensive, copyright, unsafe_food, sexual, other"
	}
	details := strings.TrimSpace(req.Details)
	if len([]rune(details)) > maxReportDetails {
		problems["details"] = "Не более 2000 символов"
	}
	// Reporting yourself is not a moderation signal, it is a mistake.
	if req.TargetType == "user" && req.TargetID == caller.String() {
		problems["target_id"] = "Нельзя пожаловаться на себя"
	}
	if len(problems) > 0 {
		httpx.Respond(w, r, httpx.Validation(problems))
		return
	}

	id, err := a.mod.Report(r.Context(), caller.String(), moderation.ReportInput{
		TargetType: req.TargetType,
		TargetID:   req.TargetID,
		Reason:     req.Reason,
		Details:    details,
	})
	switch {
	case errors.Is(err, moderation.ErrDuplicate):
		// The partial unique index stopped a second open report on the same
		// target. That is the intended outcome, not a failure the user caused,
		// so it reads as "already received" rather than an error to retry.
		httpx.Respond(w, r, httpx.Conflict("Жалоба уже отправлена и рассматривается"))
		return
	case errors.Is(err, moderation.ErrNotFound):
		httpx.Respond(w, r, httpx.NotFound("Объект жалобы не найден"))
		return
	case err != nil:
		httpx.Respond(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, reportResponse{ID: id})
}

func (a *API) handleBlock(w http.ResponseWriter, r *http.Request) {
	caller, target, ok := a.blockTarget(w, r)
	if !ok {
		return
	}
	if err := a.mod.Block(r.Context(), caller.String(), target.String()); err != nil {
		if errors.Is(err, moderation.ErrNotFound) {
			httpx.Respond(w, r, httpx.NotFound("Пользователь не найден"))
			return
		}
		httpx.Respond(w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (a *API) handleUnblock(w http.ResponseWriter, r *http.Request) {
	caller, target, ok := a.blockTarget(w, r)
	if !ok {
		return
	}
	if err := a.mod.Unblock(r.Context(), caller.String(), target.String()); err != nil {
		httpx.Respond(w, r, err)
		return
	}
	httpx.NoContent(w)
}

// blockTarget resolves the caller and the user being blocked, writing the
// response itself and reporting false when the request cannot proceed.
func (a *API) blockTarget(w http.ResponseWriter, r *http.Request) (caller, target uuid.UUID, ok bool) {
	caller, ok = a.caller(r)
	if !ok {
		httpx.Respond(w, r, httpx.Unauthorized("Нужен вход в аккаунт"))
		return uuid.UUID{}, uuid.UUID{}, false
	}
	target, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		httpx.Respond(w, r, httpx.BadRequest("Некорректный идентификатор пользователя"))
		return uuid.UUID{}, uuid.UUID{}, false
	}
	// user_blocks carries a CHECK against self-blocking; catching it here gives
	// a readable message instead of a constraint violation.
	if target == caller {
		httpx.Respond(w, r, httpx.Validation(map[string]string{
			"id": "Нельзя заблокировать самого себя",
		}))
		return uuid.UUID{}, uuid.UUID{}, false
	}
	return caller, target, true
}

// BlockedUser is one row of the caller's block list.
type BlockedUser struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	BlockedAt   string `json:"blocked_at"`
}

type blockListResponse struct {
	Items []BlockedUser `json:"items"`
}

// handleListBlocks lets a user see and undo their own blocks, which is the
// half of Guideline 1.2 that is easy to forget: a block nobody can find again
// is a trap, not a control.
func (a *API) handleListBlocks(w http.ResponseWriter, r *http.Request) {
	caller, ok := a.caller(r)
	if !ok {
		httpx.Respond(w, r, httpx.Unauthorized("Нужен вход в аккаунт"))
		return
	}
	items, err := a.mod.ListBlocked(r.Context(), caller.String())
	if err != nil {
		httpx.Respond(w, r, err)
		return
	}
	out := make([]BlockedUser, 0, len(items))
	for _, it := range items {
		out = append(out, BlockedUser{
			ID: it.ID, Handle: it.Handle,
			DisplayName: it.DisplayName, BlockedAt: it.BlockedAt,
		})
	}
	httpx.JSON(w, http.StatusOK, blockListResponse{Items: out})
}
