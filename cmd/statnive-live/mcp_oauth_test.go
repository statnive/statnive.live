//go:build chatgpt_app

package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/auth"
)

const (
	testIssuer   = "https://idp.example.com"
	testAudience = "https://mcp.statnive.live"
	testKID      = "test-key-1"
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func newTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}

	return k
}

// mintRS256 builds a signed RS256 JWT from the given claims.
func mintRS256(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()

	hdr, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid})
	pay, _ := json.Marshal(claims)
	signingInput := b64(hdr) + "." + b64(pay)
	sum := sha256.Sum256([]byte(signingInput))

	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	return signingInput + "." + b64(sig)
}

func seededCache(pub *rsa.PublicKey, kid string) *jwksCache {
	now := time.Now()

	// lastAttempt set so a cache miss (unknown kid) is rate-limited and does
	// NOT try to dial (this cache has no http client).
	return &jwksCache{keys: map[string]any{kid: pub}, lastFetch: now, lastAttempt: now}
}

func goodConfig() mcpOAuthConfig {
	return mcpOAuthConfig{Enabled: true, Issuer: testIssuer, Audience: testAudience}
}

func goodClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": testIssuer,
		"aud": testAudience,
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
		"sub": "user-123",
	}
}

func TestVerifyToken_Valid(t *testing.T) {
	t.Parallel()

	key := newTestKey(t)
	now := time.Unix(1_750_000_000, 0)
	tok := mintRS256(t, key, testKID, goodClaims(now))

	if err := verifyToken(tok, goodConfig(), seededCache(&key.PublicKey, testKID), now); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
}

func TestVerifyToken_Rejections(t *testing.T) {
	t.Parallel()

	key := newTestKey(t)
	now := time.Unix(1_750_000_000, 0)
	cache := seededCache(&key.PublicKey, testKID)

	cases := []struct {
		name string
		tok  func() string
		cfg  mcpOAuthConfig
	}{
		{"expired", func() string {
			c := goodClaims(now)
			c["exp"] = now.Add(-time.Hour).Unix()

			return mintRS256(t, key, testKID, c)
		}, goodConfig()},
		{"wrong audience", func() string {
			c := goodClaims(now)
			c["aud"] = "https://evil.example.com"

			return mintRS256(t, key, testKID, c)
		}, goodConfig()},
		{"wrong issuer", func() string {
			c := goodClaims(now)
			c["iss"] = "https://evil.example.com"

			return mintRS256(t, key, testKID, c)
		}, goodConfig()},
		{"alg none", func() string {
			hdr, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
			pay, _ := json.Marshal(goodClaims(now))

			return b64(hdr) + "." + b64(pay) + "."
		}, goodConfig()},
		{"tampered signature", func() string {
			tok := mintRS256(t, key, testKID, goodClaims(now))

			return tok[:len(tok)-3] + "AAA"
		}, goodConfig()},
		{"wrong signing key", func() string {
			other := newTestKey(t)

			return mintRS256(t, other, testKID, goodClaims(now))
		}, goodConfig()},
		{"missing required scope", func() string {
			return mintRS256(t, key, testKID, goodClaims(now))
		}, mcpOAuthConfig{Enabled: true, Issuer: testIssuer, Audience: testAudience, RequiredScope: "analytics:read"}},
		{"unknown kid", func() string {
			return mintRS256(t, key, "rotated-away", goodClaims(now))
		}, goodConfig()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := verifyToken(tc.tok(), tc.cfg, cache, now); err == nil {
				t.Errorf("%s: token accepted, want rejection", tc.name)
			}
		})
	}
}

func TestVerifyToken_ScopePresent(t *testing.T) {
	t.Parallel()

	key := newTestKey(t)
	now := time.Unix(1_750_000_000, 0)
	c := goodClaims(now)
	c["scope"] = "openid analytics:read profile"
	tok := mintRS256(t, key, testKID, c)

	cfg := mcpOAuthConfig{Enabled: true, Issuer: testIssuer, Audience: testAudience, RequiredScope: "analytics:read"}
	if err := verifyToken(tok, cfg, seededCache(&key.PublicKey, testKID), now); err != nil {
		t.Errorf("token with the required scope rejected: %v", err)
	}
}

// jwksJSON renders an RSA public key as a one-key JWKS document.
func jwksJSON(pub *rsa.PublicKey, kid string) []byte {
	n := b64(pub.N.Bytes())
	e := b64(big.NewInt(int64(pub.E)).Bytes())
	body, _ := json.Marshal(map[string]any{
		"keys": []map[string]string{{"kty": "RSA", "kid": kid, "n": n, "e": e}},
	})

	return body
}

// TestOAuthMiddleware_EndToEnd exercises the full middleware incl. the JWKS
// fetch: valid token → handler reached with an authed user; missing/expired →
// 401 with a WWW-Authenticate discovery hint.
func TestOAuthMiddleware_EndToEnd(t *testing.T) {
	t.Parallel()

	key := newTestKey(t)

	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwksJSON(&key.PublicKey, testKID))
	}))
	defer jwks.Close()

	cfg := mcpOAuthConfig{
		Enabled: true, Issuer: testIssuer, Audience: testAudience,
		JWKSURL: jwks.URL, ResourceMetadataURL: "https://mcp.statnive.live/.well-known/oauth-protected-resource",
		AllowedSiteIDs: []uint32{1, 4},
	}

	mw, err := oauthMiddleware(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("oauthMiddleware: %v", err)
	}

	var sawScopedUser bool

	guarded := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The principal must be SCOPED to the allowed sites — never wildcard.
		u := auth.UserFrom(r.Context())
		sawScopedUser = u != nil && u.ActorCanReadSite(1) && u.ActorCanReadSite(4) && !u.ActorCanReadSite(99)

		w.WriteHeader(http.StatusOK)
	}))

	srv := httptest.NewServer(guarded)
	defer srv.Close()

	// Valid token → 200 + authed user.
	tok := mintRS256(t, key, testKID, goodClaims(time.Now()))
	if code := doAuth(t, srv.URL, "Bearer "+tok); code != http.StatusOK {
		t.Errorf("valid token: status = %d, want 200", code)
	}

	if !sawScopedUser {
		t.Error("handler did not see a correctly site-scoped (non-wildcard) user")
	}

	// Missing token → 401.
	if code := doAuth(t, srv.URL, ""); code != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", code)
	}

	// Expired token → 401.
	expired := goodClaims(time.Now())
	expired["exp"] = time.Now().Add(-time.Hour).Unix()

	if code := doAuth(t, srv.URL, "Bearer "+mintRS256(t, key, testKID, expired)); code != http.StatusUnauthorized {
		t.Errorf("expired token: status = %d, want 401", code)
	}
}

func doAuth(t *testing.T, url, authz string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if authz != "" {
		req.Header.Set("Authorization", authz)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		if wa := resp.Header.Get("WWW-Authenticate"); !strings.Contains(wa, "Bearer") {
			t.Errorf("401 missing WWW-Authenticate: %q", wa)
		}
	}

	return resp.StatusCode
}
