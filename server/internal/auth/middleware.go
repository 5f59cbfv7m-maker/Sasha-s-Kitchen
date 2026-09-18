package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
)

// Identity is the authenticated caller, as handlers see them.
type Identity struct {
	UserID      uuid.UUID
	Handle      string
	DisplayName string
	IsAdmin     bool
	IsAuthor    bool
	// TokenID is the jti of the access token, useful for correlating a request
	// with the session that made it.
	TokenID string
}

// identityKey is an unexported context key, so nothing outside this package can
// fabricate an identity by writing to the context directly.
type identityKey struct{}

// UserFrom returns the authenticated caller, if there is one. Handlers behind
// RequireAuth can rely on ok being true; handlers behind Optional must check.
func UserFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// withIdentity attaches an identity to the request context.
func withIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// Authenticator turns bearer tokens into identities.
type Authenticator struct {
	tokens *TokenService
	repo   *Repo
}

// NewAuthenticator wires the middleware over the same token service and
// repository the service layer uses.
func NewAuthenticator(tokens *TokenService, repo *Repo) *Authenticator {
	return &Authenticator{tokens: tokens, repo: repo}
}

// RequireAuth rejects anonymous or invalid callers with 401.
func (a *Authenticator) RequireAuth() httpx.Middleware {
	return a.guard(func(Identity) error { return nil })
}

// RequireAdmin additionally requires the admin flag.
func (a *Authenticator) RequireAdmin() httpx.Middleware {
	return a.guard(func(id Identity) error {
		if !id.IsAdmin {
			return httpx.Forbidden("Доступ только для администраторов")
		}
		return nil
	})
}

// RequireAuthor additionally requires the author flag. Admins pass too: an
// admin who cannot open an author screen cannot moderate it either.
func (a *Authenticator) RequireAuthor() httpx.Middleware {
	return a.guard(func(id Identity) error {
		if !id.IsAuthor && !id.IsAdmin {
			return httpx.Forbidden("Доступ только для авторов рецептов")
		}
		return nil
	})
}

// Optional attaches an identity when the request carries a usable token and
// otherwise carries on anonymously. The storefront is browsable without an
// account, so a stale token must degrade to "anonymous", never to an error.
func (a *Authenticator) Optional() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			id, err := a.identify(r.Context(), token)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), id)))
		})
	}
}

// guard builds a middleware that authenticates and then applies one extra check.
func (a *Authenticator) guard(check func(Identity) error) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
				httpx.Respond(w, r, httpx.Unauthorized("Требуется авторизация"))
				return
			}
			id, err := a.identify(r.Context(), token)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="api", error="invalid_token"`)
				httpx.Respond(w, r, err)
				return
			}
			if err := check(id); err != nil {
				httpx.Respond(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), id)))
		})
	}
}

// identify validates the token and confirms the account is still usable.
//
// The account is re-read on every authenticated request rather than trusted
// from the token's claims. That is the only way "a suspended or deleted user's
// tokens stop working immediately" can hold: an access token is valid for
// minutes, and any cache here — however short — is a window in which a banned
// account keeps posting. The read is a primary-key lookup, which is the
// cheapest query the database has.
func (a *Authenticator) identify(ctx context.Context, token string) (Identity, error) {
	claims, err := a.tokens.ParseAccess(token)
	if err != nil {
		return Identity{}, httpx.Unauthorized("Недействительный токен доступа").WithCause(err)
	}
	userID, err := claims.SubjectID()
	if err != nil {
		return Identity{}, httpx.Unauthorized("Недействительный токен доступа").WithCause(err)
	}

	user, err := a.repo.FindByID(ctx, userID)
	switch {
	case errors.Is(err, ErrUserNotFound):
		// Deleted accounts are gone from every lookup, so this covers deletion.
		return Identity{}, httpx.Unauthorized("Аккаунт недоступен")
	case err != nil:
		return Identity{}, httpx.Internal("Не удалось проверить авторизацию").WithCause(err)
	}
	if err := checkUsable(user); err != nil {
		return Identity{}, err
	}

	return Identity{
		UserID:      user.ID,
		Handle:      user.Handle,
		DisplayName: user.DisplayName,
		IsAdmin:     user.IsAdmin,
		IsAuthor:    user.IsAuthor,
		TokenID:     claims.ID,
	}, nil
}

// bearerToken extracts the credential from an Authorization header.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}
	scheme, value, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
}
