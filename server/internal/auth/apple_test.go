package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAppleClientID = "com.kirillrychkov.FridgeOracle"

// appleKey is one signing key plus the kid Apple would publish it under.
type appleKey struct {
	kid string
	key *rsa.PrivateKey
}

func newAppleKey(t *testing.T, kid string) appleKey {
	t.Helper()
	// 2048 is Apple's real size; smaller would not exercise the same decoding.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return appleKey{kid: kid, key: key}
}

// jwksServer serves a JWKS document whose key set can be swapped mid-test, so
// key rotation can be exercised. It counts requests so caching can be asserted.
type jwksServer struct {
	*httptest.Server
	mu   sync.Mutex
	keys []appleKey
	hits atomic.Int64
	fail atomic.Bool
}

func newJWKSServer(t *testing.T, keys ...appleKey) *jwksServer {
	t.Helper()
	s := &jwksServer{keys: keys}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.hits.Add(1)
		if s.fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		current := append([]appleKey(nil), s.keys...)
		s.mu.Unlock()

		type jwk struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		}
		doc := struct {
			Keys []jwk `json:"keys"`
		}{}
		for _, k := range current {
			doc.Keys = append(doc.Keys, jwk{
				Kty: "RSA",
				Kid: k.kid,
				Use: "sig",
				Alg: "RS256",
				N:   base64.RawURLEncoding.EncodeToString(k.key.N.Bytes()),
				E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.key.E)).Bytes()),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *jwksServer) rotate(keys ...appleKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = keys
}

// appleToken mints an identity token the way Apple would.
type appleToken struct {
	subject  string
	audience string
	issuer   string
	email    string
	verified any
	nonce    string
	issuedAt time.Time
	expires  time.Time
}

func (tok appleToken) sign(t *testing.T, k appleKey) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": tok.issuer,
		"aud": tok.audience,
		"sub": tok.subject,
		"iat": tok.issuedAt.Unix(),
		"exp": tok.expires.Unix(),
	}
	if tok.email != "" {
		claims["email"] = tok.email
		claims["email_verified"] = tok.verified
	}
	if tok.nonce != "" {
		claims["nonce"] = tok.nonce
	}
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	jwtToken.Header["kid"] = k.kid
	signed, err := jwtToken.SignedString(k.key)
	require.NoError(t, err)
	return signed
}

func defaultToken(now time.Time) appleToken {
	return appleToken{
		subject:  "001234.abcdef0123456789.0000",
		audience: testAppleClientID,
		issuer:   appleIssuer,
		email:    "chef@privaterelay.appleid.com",
		verified: "true",
		nonce:    "nonce-from-the-client",
		issuedAt: now.Add(-time.Minute),
		expires:  now.Add(10 * time.Minute),
	}
}

func newTestVerifier(server *jwksServer, now func() time.Time) *AppleVerifier {
	return NewAppleVerifier(testAppleClientID, "TEAMID1234", AppleOptions{
		HTTPClient: server.Client(),
		JWKSURL:    server.URL,
		Now:        now,
	})
}

func TestAppleVerifyAcceptsGenuineToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	key := newAppleKey(t, "key-1")
	server := newJWKSServer(t, key)
	verifier := newTestVerifier(server, clock.Now)

	tests := []struct {
		name         string
		mutate       func(*appleToken)
		wantEmail    string
		wantVerified bool
	}{
		{
			name:         "verified email as string",
			mutate:       func(*appleToken) {},
			wantEmail:    "chef@privaterelay.appleid.com",
			wantVerified: true,
		},
		{
			name:         "verified email as bool",
			mutate:       func(tok *appleToken) { tok.verified = true },
			wantEmail:    "chef@privaterelay.appleid.com",
			wantVerified: true,
		},
		{
			name:         "unverified email",
			mutate:       func(tok *appleToken) { tok.verified = false },
			wantEmail:    "chef@privaterelay.appleid.com",
			wantVerified: false,
		},
		{
			name:         "no email claim",
			mutate:       func(tok *appleToken) { tok.email = "" },
			wantEmail:    "",
			wantVerified: false,
		},
		{
			name:         "mixed case email is normalised",
			mutate:       func(tok *appleToken) { tok.email = "Chef@PrivateRelay.AppleID.com" },
			wantEmail:    "chef@privaterelay.appleid.com",
			wantVerified: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tok := defaultToken(clock.Now())
			tc.mutate(&tok)

			identity, err := verifier.Verify(ctx, tok.sign(t, key), "nonce-from-the-client")
			require.NoError(t, err)
			assert.Equal(t, tok.subject, identity.Subject)
			assert.Equal(t, tc.wantEmail, identity.Email)
			assert.Equal(t, tc.wantVerified, identity.EmailVerified)
		})
	}
}

func TestAppleVerifyRejectsBadTokens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	key := newAppleKey(t, "key-1")
	foreign := newAppleKey(t, "key-1") // same kid, attacker's key
	server := newJWKSServer(t, key)
	verifier := newTestVerifier(server, clock.Now)

	tests := []struct {
		name  string
		token func(t *testing.T) string
		nonce string
	}{
		{
			name:  "empty token",
			token: func(*testing.T) string { return "" },
			nonce: "nonce-from-the-client",
		},
		{
			name:  "not a jwt",
			token: func(*testing.T) string { return "gibberish" },
			nonce: "nonce-from-the-client",
		},
		{
			name: "signed by another key",
			token: func(t *testing.T) string {
				return defaultToken(clock.Now()).sign(t, foreign)
			},
			nonce: "nonce-from-the-client",
		},
		{
			name: "wrong audience",
			token: func(t *testing.T) string {
				tok := defaultToken(clock.Now())
				tok.audience = "com.someone.else"
				return tok.sign(t, key)
			},
			nonce: "nonce-from-the-client",
		},
		{
			name: "wrong issuer",
			token: func(t *testing.T) string {
				tok := defaultToken(clock.Now())
				tok.issuer = "https://accounts.google.com"
				return tok.sign(t, key)
			},
			nonce: "nonce-from-the-client",
		},
		{
			name: "expired",
			token: func(t *testing.T) string {
				tok := defaultToken(clock.Now())
				tok.issuedAt = clock.Now().Add(-2 * time.Hour)
				tok.expires = clock.Now().Add(-time.Hour)
				return tok.sign(t, key)
			},
			nonce: "nonce-from-the-client",
		},
		{
			name: "nonce mismatch",
			token: func(t *testing.T) string {
				tok := defaultToken(clock.Now())
				tok.nonce = "some-other-nonce"
				return tok.sign(t, key)
			},
			nonce: "nonce-from-the-client",
		},
		{
			name: "nonce missing when one was expected",
			token: func(t *testing.T) string {
				tok := defaultToken(clock.Now())
				tok.nonce = ""
				return tok.sign(t, key)
			},
			nonce: "nonce-from-the-client",
		},
		{
			name: "no subject",
			token: func(t *testing.T) string {
				tok := defaultToken(clock.Now())
				tok.subject = ""
				return tok.sign(t, key)
			},
			nonce: "nonce-from-the-client",
		},
		{
			name: "unknown signing key",
			token: func(t *testing.T) string {
				return defaultToken(clock.Now()).sign(t, newAppleKey(t, "key-never-published"))
			},
			nonce: "nonce-from-the-client",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := verifier.Verify(ctx, tc.token(t), tc.nonce)
			require.Error(t, err, "token %q must be rejected", tc.name)
			require.ErrorIs(t, err, ErrAppleToken)
		})
	}
}

func TestAppleVerifyRejectsAlgorithmConfusion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	key := newAppleKey(t, "key-1")
	server := newJWKSServer(t, key)
	verifier := newTestVerifier(server, clock.Now)

	// "alg":"none", assembled by hand because no library will sign it.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"key-1","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"iss":"` + appleIssuer + `","aud":"` + testAppleClientID + `","sub":"001.forged","exp":99999999999}`))

	_, err := verifier.Verify(ctx, header+"."+payload+".", "")
	require.ErrorIs(t, err, ErrAppleToken)

	// HS256 keyed on the public modulus — the other half of the confusion pair.
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": appleIssuer, "aud": testAppleClientID, "sub": "001.forged",
		"exp": clock.Now().Add(time.Hour).Unix(),
	})
	hs.Header["kid"] = "key-1"
	signed, err := hs.SignedString(key.key.N.Bytes())
	require.NoError(t, err)

	_, err = verifier.Verify(ctx, signed, "")
	require.ErrorIs(t, err, ErrAppleToken)
}

func TestAppleVerifySkipsNonceCheckWhenNoneExpected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	key := newAppleKey(t, "key-1")
	server := newJWKSServer(t, key)
	verifier := newTestVerifier(server, clock.Now)

	tok := defaultToken(clock.Now())
	tok.nonce = "whatever-the-client-sent"

	identity, err := verifier.Verify(ctx, tok.sign(t, key), "")
	require.NoError(t, err)
	assert.Equal(t, tok.subject, identity.Subject)
}

func TestAppleJWKSCachedAndRotated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	oldKey := newAppleKey(t, "key-old")
	server := newJWKSServer(t, oldKey)
	verifier := newTestVerifier(server, clock.Now)

	tok := defaultToken(clock.Now())
	_, err := verifier.Verify(ctx, tok.sign(t, oldKey), tok.nonce)
	require.NoError(t, err)
	require.EqualValues(t, 1, server.hits.Load())

	// Repeat verifications inside the cache TTL must not hit Apple again.
	for range 5 {
		_, err = verifier.Verify(ctx, tok.sign(t, oldKey), tok.nonce)
		require.NoError(t, err)
	}
	assert.EqualValues(t, 1, server.hits.Load(), "the key set must be cached")

	// Apple rotates. A token signed by the new key carries an unknown kid, and
	// the verifier must refetch rather than reject it.
	newKey := newAppleKey(t, "key-new")
	server.rotate(oldKey, newKey)
	clock.Advance(2 * appleMinRefetchGap)

	_, err = verifier.Verify(ctx, tok.sign(t, newKey), tok.nonce)
	require.NoError(t, err, "an unknown kid must trigger a refetch")
	assert.EqualValues(t, 2, server.hits.Load())

	// The old key is still published, so tokens signed before the rotation keep
	// working.
	_, err = verifier.Verify(ctx, tok.sign(t, oldKey), tok.nonce)
	require.NoError(t, err)
}

func TestAppleUnknownKidDoesNotRefetchPerAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	key := newAppleKey(t, "key-1")
	server := newJWKSServer(t, key)
	verifier := newTestVerifier(server, clock.Now)

	tok := defaultToken(clock.Now())
	_, err := verifier.Verify(ctx, tok.sign(t, key), tok.nonce)
	require.NoError(t, err)
	require.EqualValues(t, 1, server.hits.Load())

	// A burst of forged kids must not become a burst of outbound requests.
	for range 20 {
		_, err = verifier.Verify(ctx, tok.sign(t, newAppleKey(t, "forged")), tok.nonce)
		require.Error(t, err)
	}
	assert.EqualValues(t, 1, server.hits.Load(),
		"the refetch floor must absorb an unknown-kid flood")
}

func TestAppleFallsBackToCachedKeyWhenJWKSUnavailable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	key := newAppleKey(t, "key-1")
	server := newJWKSServer(t, key)
	verifier := newTestVerifier(server, clock.Now)

	tok := defaultToken(clock.Now())
	_, err := verifier.Verify(ctx, tok.sign(t, key), tok.nonce)
	require.NoError(t, err)

	// Apple goes down and the cache goes stale. A key we already hold must keep
	// verifying rather than taking every sign-in down with it.
	server.fail.Store(true)
	clock.Advance(2 * appleKeyCacheTTL)

	stale := defaultToken(clock.Now())
	_, err = verifier.Verify(ctx, stale.sign(t, key), stale.nonce)
	require.NoError(t, err, "a cached key must survive a JWKS outage")
}

func TestAppleVerifierFailsClosedWithNoKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newClock(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	key := newAppleKey(t, "key-1")
	server := newJWKSServer(t, key)
	server.fail.Store(true)
	verifier := newTestVerifier(server, clock.Now)

	tok := defaultToken(clock.Now())
	_, err := verifier.Verify(ctx, tok.sign(t, key), tok.nonce)
	require.ErrorIs(t, err, ErrAppleToken,
		"with no cached key and no JWKS, verification must fail rather than pass")
}

func TestRSAKeyFromJWK(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		n, e    string
		wantErr bool
	}{
		{"empty modulus", "", "AQAB", true},
		{"empty exponent", base64.RawURLEncoding.EncodeToString([]byte{1, 2, 3}), "", true},
		{"invalid base64", "!!!!", "AQAB", true},
		{"zero exponent", base64.RawURLEncoding.EncodeToString([]byte{1, 2, 3}), base64.RawURLEncoding.EncodeToString([]byte{0}), true},
		{"valid", base64.RawURLEncoding.EncodeToString([]byte{1, 2, 3}), "AQAB", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key, err := rsaKeyFromJWK(tc.n, tc.e)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 65537, key.E)
		})
	}
}

func TestSanitizeHandle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"email local part", "chef.anna@privaterelay.appleid.com", "chef_anna"},
		{"already clean", "kirill", "kirill"},
		{"uppercase folded", "KIRILL", "kirill"},
		{"dashes become underscores", "anna-maria@x.com", "anna_maria"},
		{"cyrillic dropped", "саша@x.com", ""},
		{"leading separators trimmed", "...bob@x.com", "bob"},
		{"apple subject digits", "001234.abcdef.0000", "001234_abcdef_0000"},
		{"empty", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeHandle(tc.input)
			assert.Equal(t, tc.want, got)
			if got != "" && len(got) >= 3 {
				assert.True(t, handlePattern.MatchString(got),
					"a non-empty sanitised handle must satisfy the database CHECK, got %q", got)
			}
		})
	}
}
