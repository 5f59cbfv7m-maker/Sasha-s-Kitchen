package auth

import (
	"context"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
)

// Apple's published endpoints and issuer.
const (
	AppleJWKSURL = "https://appleid.apple.com/auth/keys"
	appleIssuer  = "https://appleid.apple.com"
)

// JWKS cache timing. Apple rotates signing keys without notice, so the cache
// must be short enough to pick up a rotation and guarded so that a burst of
// unknown-kid tokens cannot turn into a burst of outbound requests.
const (
	appleKeyCacheTTL   = time.Hour
	appleMinRefetchGap = time.Minute
	appleJWKSMaxBytes  = 1 << 20
)

// ErrAppleToken means the identity token failed verification. The cause is
// never shown to the client: every failure is one 401.
var ErrAppleToken = errors.New("auth: apple identity token rejected")

// AppleIdentity is what a verified identity token tells us about the user.
type AppleIdentity struct {
	Subject       string
	Email         string
	EmailVerified bool
	IsPrivate     bool
}

// AppleOptions holds the injectable pieces. Leaving them zero gives production
// behaviour; tests supply an httptest server and a fixed clock, which is how
// this code is exercised without reaching Apple.
type AppleOptions struct {
	HTTPClient *http.Client
	JWKSURL    string
	Now        func() time.Time
}

// AppleVerifier validates Sign in with Apple identity tokens against Apple's
// published JWKS.
type AppleVerifier struct {
	clientID string
	teamID   string
	jwksURL  string
	client   *http.Client
	now      func() time.Time

	// mu guards the key cache and is held across the fetch, which collapses a
	// thundering herd of cache misses into a single request.
	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// NewAppleVerifier builds a verifier for one client (bundle) id.
func NewAppleVerifier(clientID, teamID string, opts AppleOptions) *AppleVerifier {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	url := opts.JWKSURL
	if url == "" {
		url = AppleJWKSURL
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &AppleVerifier{
		clientID: clientID,
		teamID:   teamID,
		jwksURL:  url,
		client:   client,
		now:      now,
		keys:     map[string]*rsa.PublicKey{},
	}
}

// appleClaims is the identity-token payload we care about.
type appleClaims struct {
	jwt.RegisteredClaims
	Email          string   `json:"email"`
	EmailVerified  flexBool `json:"email_verified"`
	IsPrivateEmail flexBool `json:"is_private_email"`
	Nonce          string   `json:"nonce"`
}

// flexBool decodes a boolean that Apple sends sometimes as true and sometimes
// as the string "true". Both appear in real tokens.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var asBool bool
	if err := json.Unmarshal(data, &asBool); err == nil {
		*b = flexBool(asBool)
		return nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err != nil {
		return fmt.Errorf("auth: apple boolean claim: %w", err)
	}
	*b = flexBool(asString == "true")
	return nil
}

// Verify checks the identity token's signature and claims.
//
// expectedNonce is the value the client sent to Apple, hashed or not; when it
// is empty the check is skipped, which is only correct for flows that do not
// use one. The signature check pins RS256 so a token cannot arrive claiming a
// symmetric algorithm keyed on a public value.
func (v *AppleVerifier) Verify(ctx context.Context, idToken, expectedNonce string) (AppleIdentity, error) {
	if strings.TrimSpace(idToken) == "" {
		return AppleIdentity{}, fmt.Errorf("%w: empty token", ErrAppleToken)
	}

	claims := &appleClaims{}
	parsed, err := jwt.ParseWithClaims(idToken, claims,
		func(token *jwt.Token) (any, error) {
			kid, _ := token.Header["kid"].(string)
			if kid == "" {
				return nil, errors.New("missing kid header")
			}
			return v.keyFor(ctx, kid)
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(appleIssuer),
		jwt.WithAudience(v.clientID),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(v.now),
	)
	if err != nil {
		return AppleIdentity{}, fmt.Errorf("%w: %v", ErrAppleToken, err)
	}
	if !parsed.Valid {
		return AppleIdentity{}, fmt.Errorf("%w: token invalid", ErrAppleToken)
	}
	if claims.Subject == "" {
		return AppleIdentity{}, fmt.Errorf("%w: missing subject", ErrAppleToken)
	}
	if expectedNonce != "" {
		// Constant time: the nonce ties this token to one in-flight sign-in, and
		// a timing leak would let an attacker reconstruct it.
		if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(expectedNonce)) != 1 {
			return AppleIdentity{}, fmt.Errorf("%w: nonce mismatch", ErrAppleToken)
		}
	}

	return AppleIdentity{
		Subject:       claims.Subject,
		Email:         strings.ToLower(strings.TrimSpace(claims.Email)),
		EmailVerified: bool(claims.EmailVerified),
		IsPrivate:     bool(claims.IsPrivateEmail),
	}, nil
}

// keyFor returns the signing key for kid, refreshing the cache when the key is
// unknown or stale.
func (v *AppleVerifier) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	now := v.now()
	fresh := now.Sub(v.fetchedAt) < appleKeyCacheTTL
	if key, ok := v.keys[kid]; ok && fresh {
		return key, nil
	}
	// An unknown kid means either a rotation or a forged header. Refetching
	// handles the first; the rate floor keeps the second from becoming an
	// outbound request per attempt.
	if !fresh || now.Sub(v.fetchedAt) >= appleMinRefetchGap {
		if err := v.refreshKeysLocked(ctx); err != nil {
			// A stale key still verifies tokens signed before the rotation, so
			// a fetch failure is only fatal when we have nothing cached.
			if key, ok := v.keys[kid]; ok {
				return key, nil
			}
			return nil, err
		}
	}
	key, ok := v.keys[kid]
	if !ok {
		return nil, fmt.Errorf("unknown signing key %q", kid)
	}
	return key, nil
}

// refreshKeysLocked fetches and replaces the key set. The caller holds v.mu.
func (v *AppleVerifier) refreshKeysLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("auth: build jwks request: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("auth: fetch jwks: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, appleJWKSMaxBytes))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth: fetch jwks: status %d", resp.StatusCode)
	}

	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, appleJWKSMaxBytes)).Decode(&doc); err != nil {
		return fmt.Errorf("auth: decode jwks: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || (k.Alg != "" && k.Alg != "RS256") || k.Kid == "" {
			continue
		}
		key, err := rsaKeyFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = key
	}
	if len(keys) == 0 {
		return errors.New("auth: jwks contained no usable RSA keys")
	}

	v.keys = keys
	v.fetchedAt = v.now()
	return nil
}

// rsaKeyFromJWK rebuilds a public key from the base64url modulus and exponent.
func rsaKeyFromJWK(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("auth: jwk modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("auth: jwk exponent: %w", err)
	}
	if len(nBytes) == 0 || len(eBytes) == 0 || len(eBytes) > 8 {
		return nil, errors.New("auth: jwk has unusable parameters")
	}
	// The exponent is big-endian and unpadded; left-pad it into a uint64.
	padded := make([]byte, 8)
	copy(padded[8-len(eBytes):], eBytes)
	exp := binary.BigEndian.Uint64(padded)
	if exp == 0 || exp > 1<<31-1 {
		return nil, errors.New("auth: jwk exponent out of range")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(exp)}, nil
}

// ------------------------------------------------------------- sign-in --

// AppleSignInInput is the request body of POST /auth/apple.
type AppleSignInInput struct {
	IdentityToken string
	Nonce         string
	DisplayName   string
}

// SignInWithApple verifies an identity token and links or creates the account.
//
// Order matters: an existing link wins over an email match, so a user who
// changed their Apple relay address still lands on the same account.
func (s *Service) SignInWithApple(ctx context.Context, in AppleSignInInput, meta SessionMeta) (Session, error) {
	identity, err := s.apple.Verify(ctx, in.IdentityToken, in.Nonce)
	if err != nil {
		return Session{}, httpx.Unauthorized("Не удалось подтвердить вход через Apple").WithCause(err)
	}

	user, err := s.repo.FindUserByExternalIdentity(ctx, ProviderApple, identity.Subject)
	switch {
	case err == nil:
		if err := checkUsable(user); err != nil {
			return Session{}, err
		}
		// Keep the recorded relay address current.
		_ = s.repo.LinkExternalIdentity(ctx, user.ID, ProviderApple, identity.Subject, identity.Email)
		return s.startSession(ctx, user, meta)
	case !errors.Is(err, ErrUserNotFound):
		return Session{}, err
	}

	// Only a verified address may claim an existing password account; an
	// unverified one would let anyone take over by asserting someone's email.
	if identity.Email != "" && identity.EmailVerified {
		existing, err := s.repo.FindByEmail(ctx, identity.Email)
		switch {
		case err == nil:
			if err := checkUsable(existing); err != nil {
				return Session{}, err
			}
			if err := s.repo.LinkExternalIdentity(ctx, existing.ID, ProviderApple, identity.Subject, identity.Email); err != nil {
				return Session{}, err
			}
			return s.startSession(ctx, existing, meta)
		case !errors.Is(err, ErrUserNotFound):
			return Session{}, err
		}
	}

	return s.createAppleUser(ctx, identity, in.DisplayName, meta)
}

// createAppleUser registers a brand-new account for an Apple identity.
func (s *Service) createAppleUser(ctx context.Context, identity AppleIdentity, displayName string, meta SessionMeta) (Session, error) {
	handle, err := s.freeHandle(ctx, identity)
	if err != nil {
		return Session{}, err
	}

	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = handle
	}
	if len([]rune(displayName)) > maxDisplayName {
		displayName = string([]rune(displayName)[:maxDisplayName])
	}

	// A private relay address is stored: it is the only address that works, and
	// it is what the user sees in their Apple settings.
	email := ""
	if identity.EmailVerified {
		email = identity.Email
	}

	// No password hash: this account signs in only through Apple until the user
	// sets a password. Login treats an empty hash as an unconditional failure.
	user, err := s.repo.CreateUser(ctx, NewUser{
		Handle:      handle,
		DisplayName: displayName,
		Email:       email,
	})
	if err != nil {
		return Session{}, err
	}
	if err := s.repo.LinkExternalIdentity(ctx, user.ID, ProviderApple, identity.Subject, identity.Email); err != nil {
		return Session{}, err
	}
	return s.startSession(ctx, user, meta)
}

// freeHandle derives an available handle from the Apple identity, falling back
// to random suffixes. The loop is bounded so a pathological collision streak
// cannot hold a request open.
func (s *Service) freeHandle(ctx context.Context, identity AppleIdentity) (string, error) {
	base := sanitizeHandle(identity.Email)
	if base == "" {
		base = sanitizeHandle(identity.Subject)
	}
	if len(base) < 3 {
		base = "chef"
	}

	candidates := make([]string, 0, 6)
	candidates = append(candidates, base)
	for i := 0; i < 5; i++ {
		suffix := strings.ToLower(uuid.NewString()[:6])
		trimmed := base
		if len(trimmed) > 30-len(suffix)-1 {
			trimmed = trimmed[:30-len(suffix)-1]
		}
		candidates = append(candidates, trimmed+"_"+suffix)
	}

	for _, candidate := range candidates {
		if !handlePattern.MatchString(candidate) {
			continue
		}
		taken, err := s.repo.HandleTaken(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", httpx.Internal("Не удалось подобрать логин для нового аккаунта")
}

// sanitizeHandle reduces arbitrary text to the handle alphabet. Anything that
// does not fit the users_handle_format CHECK is dropped rather than transformed,
// so the result is either valid or too short to use.
func sanitizeHandle(s string) string {
	if at := strings.IndexByte(s, '@'); at > 0 {
		s = s[:at]
	}
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '+':
			b.WriteByte('_')
		}
		if b.Len() >= 30 {
			break
		}
	}
	return strings.Trim(b.String(), "_")
}
