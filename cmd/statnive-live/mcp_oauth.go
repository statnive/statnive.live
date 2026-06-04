//go:build chatgpt_app

package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/auth"
)

// This file is the ChatGPT-app (v2.5) OAuth 2.1 resource-server verifier. It is
// compiled ONLY with `-tags chatgpt_app`, so the default + air-gap binaries
// contain zero JWKS / IdP / outbound code (see mcp_oauth_stub.go). It is a
// dependency-free stdlib JWT verifier: RS256 + ES256, with the security checks
// that matter — reject alg=none / HS* (alg-confusion), pin the key type to the
// header alg, and validate iss / aud / exp / nbf / scope.

const (
	jwksRefetchInterval = time.Minute // floor between JWKS refetches on a cache miss
	jwtLeeway           = int64(60)   // clock-skew leeway, seconds
	jwksMaxBody         = int64(1 << 20)
)

// oauthMiddleware builds the resource-server middleware: verify the Bearer
// access token on every request, or 401 with RFC 9728 discovery hints.
func oauthMiddleware(o mcpOAuthConfig, logger *slog.Logger) (func(http.Handler) http.Handler, error) {
	if o.Issuer == "" || o.Audience == "" {
		return nil, errors.New("oauth requires both issuer and audience")
	}

	jwksURL := o.JWKSURL
	if jwksURL == "" {
		jwksURL = strings.TrimRight(o.Issuer, "/") + "/.well-known/jwks.json"
	}

	cache := &jwksCache{url: jwksURL, client: &http.Client{Timeout: 5 * time.Second}}
	if err := cache.refresh(); err != nil {
		logger.Warn("mcp oauth: initial JWKS fetch failed; will retry on first request", "err", err)
	}

	// Deployment ceiling — the sites ANY token may read on this deployment,
	// built once and read-only thereafter (safe to share). NEVER wildcard.
	// buildMCPAuthChain already rejects an empty allowed_site_ids for this
	// profile. The per-request grant map is this ceiling intersected with the
	// token's own consented site_ids (M1).
	ceiling := make(map[uint32]auth.Role, len(o.AllowedSiteIDs))
	for _, id := range o.AllowedSiteIDs {
		ceiling[id] = auth.RoleAPI
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearerToken(r)
			if raw == "" {
				write401(w, o, "missing bearer token")

				return
			}

			claims, err := verifyToken(raw, o, cache, time.Now())
			if err != nil {
				logger.Info("mcp oauth: token rejected", "err", err)
				write401(w, o, "invalid token")

				return
			}

			// Non-nil UserID + Sites map ⇒ the grant-map branch of
			// ActorCanReadSite. The grants honor the token's consent (M1).
			u := &auth.User{UserID: oauthActorID, Role: auth.RoleAPI, Sites: grantsForToken(claims, ceiling)}
			next.ServeHTTP(w, r.WithContext(auth.WithSession(r.Context(), u, nil)))
		})
	}, nil
}

// grantsForToken derives the per-request read grants from the token's consented
// site_ids intersected with the deployment ceiling (M1). A token with NO
// site_ids claim (legacy / external IdP) gets the full ceiling — the pre-M1
// #186 behavior. A token WITH the claim is clamped to its consented sites, so a
// token consented to [1,3] cannot read site 2 even when the ceiling is [1,2,3];
// an empty array grants nothing (fail-closed). The ceiling itself is the
// defense-in-depth cap a forged/over-broad site_ids claim can never exceed.
func grantsForToken(c jwtClaims, ceiling map[uint32]auth.Role) map[uint32]auth.Role {
	if c.SiteIDs == nil {
		return ceiling
	}

	grants := make(map[uint32]auth.Role, len(*c.SiteIDs))

	for _, id := range *c.SiteIDs {
		if role, ok := ceiling[id]; ok {
			grants[id] = role
		}
	}

	return grants
}

// oauthActorID is a fixed non-nil sentinel so the OAuth principal takes the
// grant-map branch of ActorCanReadSite (never the uuid.Nil+SiteID==0 wildcard
// branch). Distinct from the stdio scoped actor's sentinel.
var oauthActorID = uuid.UUID{0xfe}

func write401(w http.ResponseWriter, o mcpOAuthConfig, detail string) {
	if o.ResourceMetadataURL != "" {
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Bearer resource_metadata=%q, error="invalid_token"`, o.ResourceMetadataURL))
	} else {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	}

	http.Error(w, "unauthorized: "+detail, http.StatusUnauthorized)
}

func bearerToken(r *http.Request) string {
	const p = "bearer "

	h := r.Header.Get("Authorization")
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}

	return ""
}

// --- JWT verification -------------------------------------------------------

type jwtClaims struct {
	Iss   string          `json:"iss"`
	Aud   json.RawMessage `json:"aud"` // string OR []string
	Exp   int64           `json:"exp"`
	Nbf   int64           `json:"nbf"`
	Scope string          `json:"scope"`
	Scp   []string        `json:"scp"`
	// SiteIDs is the per-token consented site list (M1) the statnive AS stamps
	// on every issued token. A nil pointer means the claim is ABSENT (a legacy
	// or external-IdP token) → the verifier falls back to the deployment
	// ceiling (the pre-M1 #186 behavior). A non-nil pointer (even an empty
	// slice) means the claim is PRESENT → grants = SiteIDs ∩ ceiling, so
	// consent is enforced per token and an empty array grants nothing.
	SiteIDs *[]uint32 `json:"site_ids"`
}

func (c jwtClaims) audiences() []string {
	if len(c.Aud) == 0 {
		return nil
	}

	var one string
	if json.Unmarshal(c.Aud, &one) == nil {
		return []string{one}
	}

	var many []string

	_ = json.Unmarshal(c.Aud, &many)

	return many
}

func (c jwtClaims) hasScope(want string) bool {
	return slices.Contains(strings.Fields(c.Scope), want) || slices.Contains(c.Scp, want)
}

func verifyToken(raw string, o mcpOAuthConfig, jwks *jwksCache, now time.Time) (jwtClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return jwtClaims{}, errors.New("malformed JWT")
	}

	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return jwtClaims{}, fmt.Errorf("decode header: %w", err)
	}

	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return jwtClaims{}, fmt.Errorf("parse header: %w", err)
	}

	// Reject alg=none, HS* and anything else — only asymmetric RS256/ES256.
	// This is the alg-confusion guard.
	if hdr.Alg != "RS256" && hdr.Alg != "ES256" {
		return jwtClaims{}, fmt.Errorf("unsupported alg %q (only RS256/ES256)", hdr.Alg)
	}

	pub, err := jwks.key(hdr.Kid)
	if err != nil {
		return jwtClaims{}, err
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return jwtClaims{}, fmt.Errorf("decode signature: %w", err)
	}

	if err := verifySignature(hdr.Alg, pub, parts[0]+"."+parts[1], sig); err != nil {
		return jwtClaims{}, err
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtClaims{}, fmt.Errorf("decode claims: %w", err)
	}

	var c jwtClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return jwtClaims{}, fmt.Errorf("parse claims: %w", err)
	}

	if err := validateClaims(c, o, now); err != nil {
		return jwtClaims{}, err
	}

	return c, nil
}

// verifySignature checks the JWT signature against the public key, pinning the
// key type to the header alg (the second half of the alg-confusion guard).
func verifySignature(alg string, pub any, signingInput string, sig []byte) error {
	sum := sha256.Sum256([]byte(signingInput))

	switch alg {
	case "RS256":
		k, ok := pub.(*rsa.PublicKey)
		if !ok {
			return errors.New("alg/key mismatch: RS256 requires an RSA key")
		}

		if err := rsa.VerifyPKCS1v15(k, crypto.SHA256, sum[:], sig); err != nil {
			return errors.New("signature invalid")
		}
	case "ES256":
		k, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("alg/key mismatch: ES256 requires an EC key")
		}

		if len(sig) != 64 {
			return errors.New("malformed ES256 signature")
		}

		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])

		if !ecdsa.Verify(k, sum[:], r, s) {
			return errors.New("signature invalid")
		}
	default:
		return fmt.Errorf("unsupported alg %q", alg)
	}

	return nil
}

// validateClaims checks the registered claims: issuer, audience, expiry,
// not-before, and (if configured) the required scope.
func validateClaims(c jwtClaims, o mcpOAuthConfig, now time.Time) error {
	if c.Iss != o.Issuer {
		return errors.New("iss mismatch")
	}

	if !slices.Contains(c.audiences(), o.Audience) {
		return errors.New("aud mismatch")
	}

	if c.Exp == 0 || now.Unix() > c.Exp+jwtLeeway {
		return errors.New("token expired")
	}

	if c.Nbf > 0 && now.Unix()+jwtLeeway < c.Nbf {
		return errors.New("token not yet valid")
	}

	if o.RequiredScope != "" && !c.hasScope(o.RequiredScope) {
		return fmt.Errorf("missing required scope %q", o.RequiredScope)
	}

	return nil
}

// --- JWKS cache -------------------------------------------------------------

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"` // RSA modulus (base64url)
	E   string `json:"e"` // RSA exponent (base64url)
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwksCache struct {
	url         string
	client      *http.Client
	mu          sync.RWMutex
	keys        map[string]any
	lastFetch   time.Time
	lastAttempt time.Time
}

const defaultKID = "_default"

func (c *jwksCache) key(kid string) (any, error) {
	if kid == "" {
		kid = defaultKID
	}

	c.mu.RLock()
	k, ok := c.keys[kid]
	c.mu.RUnlock()

	if ok {
		return k, nil
	}

	// Unknown kid → the IdP may have rotated keys. Refetch (rate-limited).
	if err := c.refresh(); err != nil {
		return nil, err
	}

	c.mu.RLock()
	k, ok = c.keys[kid]
	c.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no JWKS key for kid %q", kid)
	}

	return k, nil
}

// refresh re-fetches the JWKS. It rate-limits EVERY attempt (success or
// failure, keyed on lastAttempt set before the fetch) to once per
// jwksRefetchInterval, so a burst of unknown/rotated/attacker-supplied kids
// collapses into at most one outbound dial per interval instead of N blocking
// round-trips.
func (c *jwksCache) refresh() error {
	c.mu.Lock()

	throttled := !c.lastAttempt.IsZero() && time.Since(c.lastAttempt) < jwksRefetchInterval
	if throttled {
		c.mu.Unlock()

		return nil
	}

	c.lastAttempt = time.Now()
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("jwks fetch: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks fetch: status %d", resp.StatusCode)
	}

	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, jwksMaxBody)).Decode(&doc); err != nil {
		return fmt.Errorf("jwks decode: %w", err)
	}

	parsed := make(map[string]any, len(doc.Keys))

	for _, k := range doc.Keys {
		pub, err := parseJWK(k)
		if err != nil {
			continue // skip unusable keys (unknown kty/crv)
		}

		id := k.Kid
		if id == "" {
			id = defaultKID
		}

		parsed[id] = pub
	}

	if len(parsed) == 0 {
		return errors.New("jwks: no usable keys")
	}

	c.mu.Lock()
	c.keys = parsed
	c.lastFetch = time.Now()
	c.mu.Unlock()

	return nil
}

func parseJWK(k jwk) (any, error) {
	switch k.Kty {
	case "RSA":
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, err
		}

		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, err
		}

		e := 0
		for _, b := range eb {
			e = e<<8 | int(b)
		}

		return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}, nil
	case "EC":
		if k.Crv != "P-256" {
			return nil, fmt.Errorf("unsupported EC curve %q", k.Crv)
		}

		xb, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}

		yb, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, err
		}

		return &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xb), Y: new(big.Int).SetBytes(yb)}, nil
	default:
		return nil, fmt.Errorf("unsupported kty %q", k.Kty)
	}
}
