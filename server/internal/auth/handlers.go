package auth

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
)

// Request body caps. Credentials are small; an Apple identity token is a JWT
// with a full RSA signature, so it gets more room.
const (
	maxCredentialBody = 4 << 10
	maxAppleBody      = 16 << 10
)

// Handlers is the HTTP surface of this package.
type Handlers struct {
	svc  *Service
	auth *Authenticator
}

// NewHandlers wires the transport layer over the service.
func NewHandlers(svc *Service, auth *Authenticator) *Handlers {
	return &Handlers{svc: svc, auth: auth}
}

// Routes registers the auth endpoints on mux using Go 1.22 method patterns, so
// a wrong method produces a 405 from the router instead of reaching a handler.
func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.Handle("POST /auth/register", http.HandlerFunc(h.register))
	mux.Handle("POST /auth/login", http.HandlerFunc(h.login))
	mux.Handle("POST /auth/refresh", http.HandlerFunc(h.refresh))
	mux.Handle("POST /auth/logout", http.HandlerFunc(h.logout))
	mux.Handle("POST /auth/apple", http.HandlerFunc(h.apple))

	protected := h.auth.RequireAuth()
	mux.Handle("GET /auth/me", protected(http.HandlerFunc(h.me)))
	mux.Handle("DELETE /auth/me", protected(http.HandlerFunc(h.deleteMe)))
}

// ------------------------------------------------------------------ DTOs --

// userDTO is the public projection of a user.
//
// It exists as its own type so that password_hash has no route to a response:
// the model carries it, this does not, and nothing converts one to the other
// implicitly. The same holds for token hashes, which never leave the database.
type userDTO struct {
	ID            string    `json:"id"`
	Handle        string    `json:"handle"`
	DisplayName   string    `json:"display_name"`
	Email         string    `json:"email,omitempty"`
	Bio           string    `json:"bio,omitempty"`
	AvatarMediaID string    `json:"avatar_media_id,omitempty"`
	IsAuthor      bool      `json:"is_author"`
	IsAdmin       bool      `json:"is_admin"`
	CreatedAt     time.Time `json:"created_at"`
}

func toUserDTO(u User) userDTO {
	dto := userDTO{
		ID:          u.ID.String(),
		Handle:      u.Handle,
		DisplayName: u.DisplayName,
		Email:       u.Email,
		Bio:         u.Bio,
		IsAuthor:    u.IsAuthor,
		IsAdmin:     u.IsAdmin,
		CreatedAt:   u.CreatedAt,
	}
	if u.AvatarMediaID != uuid.Nil {
		dto.AvatarMediaID = u.AvatarMediaID.String()
	}
	return dto
}

// sessionDTO is what a successful authentication returns.
type sessionDTO struct {
	User         userDTO `json:"user"`
	TokenType    string  `json:"token_type"`
	AccessToken  string  `json:"access_token"`
	ExpiresIn    int     `json:"expires_in"`
	RefreshToken string  `json:"refresh_token"`
	RefreshUntil string  `json:"refresh_expires_at"`
}

func toSessionDTO(s Session, now time.Time) sessionDTO {
	return sessionDTO{
		User:         toUserDTO(s.User),
		TokenType:    "Bearer",
		AccessToken:  s.AccessToken,
		ExpiresIn:    int(s.AccessExpiresAt.Sub(now).Seconds()),
		RefreshToken: s.RefreshToken,
		RefreshUntil: s.RefreshExpiresAt.UTC().Format(time.RFC3339),
	}
}

// -------------------------------------------------------------- handlers --

type registerRequest struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Password    string `json:"password"`
}

func (h *Handlers) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := httpx.DecodeJSON(w, r, &req, maxCredentialBody); err != nil {
		httpx.Respond(w, r, err)
		return
	}
	session, err := h.svc.Register(r.Context(), RegisterInput{
		Handle:      req.Handle,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Password:    req.Password,
	}, metaFrom(r))
	if err != nil {
		httpx.Respond(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, toSessionDTO(session, h.svc.now()))
}

type loginRequest struct {
	// Login accepts a handle or an email; the client does not have to know
	// which one the account was created with.
	Login    string `json:"login"`
	Password string `json:"password"`
}

func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.DecodeJSON(w, r, &req, maxCredentialBody); err != nil {
		httpx.Respond(w, r, err)
		return
	}
	session, err := h.svc.Login(r.Context(), LoginInput{
		Login:    req.Login,
		Password: req.Password,
	}, metaFrom(r))
	if err != nil {
		httpx.Respond(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toSessionDTO(session, h.svc.now()))
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *Handlers) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := httpx.DecodeJSON(w, r, &req, maxCredentialBody); err != nil {
		httpx.Respond(w, r, err)
		return
	}
	session, err := h.svc.Refresh(r.Context(), req.RefreshToken, metaFrom(r))
	if err != nil {
		httpx.Respond(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toSessionDTO(session, h.svc.now()))
}

func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := httpx.DecodeJSON(w, r, &req, maxCredentialBody); err != nil {
		httpx.Respond(w, r, err)
		return
	}
	if err := h.svc.Logout(r.Context(), req.RefreshToken); err != nil {
		httpx.Respond(w, r, err)
		return
	}
	httpx.NoContent(w)
}

type appleRequest struct {
	IdentityToken string `json:"identity_token"`
	Nonce         string `json:"nonce"`
	DisplayName   string `json:"display_name"`
}

func (h *Handlers) apple(w http.ResponseWriter, r *http.Request) {
	var req appleRequest
	if err := httpx.DecodeJSON(w, r, &req, maxAppleBody); err != nil {
		httpx.Respond(w, r, err)
		return
	}
	session, err := h.svc.SignInWithApple(r.Context(), AppleSignInInput{
		IdentityToken: req.IdentityToken,
		Nonce:         req.Nonce,
		DisplayName:   req.DisplayName,
	}, metaFrom(r))
	if err != nil {
		httpx.Respond(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toSessionDTO(session, h.svc.now()))
}

func (h *Handlers) me(w http.ResponseWriter, r *http.Request) {
	id, ok := UserFrom(r.Context())
	if !ok {
		httpx.Respond(w, r, httpx.Unauthorized("Требуется авторизация"))
		return
	}
	user, err := h.svc.Me(r.Context(), id.UserID)
	if err != nil {
		httpx.Respond(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toUserDTO(user))
}

func (h *Handlers) deleteMe(w http.ResponseWriter, r *http.Request) {
	id, ok := UserFrom(r.Context())
	if !ok {
		httpx.Respond(w, r, httpx.Unauthorized("Требуется авторизация"))
		return
	}
	if err := h.svc.DeleteAccount(r.Context(), id.UserID); err != nil {
		httpx.Respond(w, r, err)
		return
	}
	httpx.NoContent(w)
}

// metaFrom records where a session was opened from. It is provenance only:
// nothing authorises on it, so a spoofed header costs nothing.
func metaFrom(r *http.Request) SessionMeta {
	return SessionMeta{
		UserAgent: truncate(r.UserAgent(), 512),
		IP:        requestIP(r),
	}
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}

// requestIP resolves the caller address for the inet column. It mirrors the
// forwarding headers httpx.RateLimitByIP trusts, and an address that will not
// parse is simply dropped by the repository.
func requestIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
		return real
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
