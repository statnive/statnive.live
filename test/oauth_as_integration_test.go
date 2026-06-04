//go:build integration && chatgpt_app

// OAuth 2.1 authorization-server integration + J-red-team suite (PR-E). Drives
// the real internal/oauthas handlers + storage against live ClickHouse. Covers
// the store invariants (single-use codes, refresh rotation + reuse-detection,
// expiry), the happy-path authorize→consent→token flow, the issuance-side M1
// site clamp, and the adversarial rejections the plan's J agent mandates:
// code-replay, cross-client code use, PKCE-strip, PKCE-downgrade,
// redirect-smuggle, refresh-reuse-after-rotate, scope + consent escalation.
//
// Build: needs BOTH tags. Run: go test -tags 'integration chatgpt_app' ./test/...
package integration_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/metrics"
	"github.com/statnive/statnive.live/internal/oauthas"
	"github.com/statnive/statnive.live/internal/storage"
)

// --- fixture ---------------------------------------------------------------

type asFixture struct {
	srv      *oauthas.Server
	store    *oauthas.Store
	key      *oauthas.SigningKey
	reg      *metrics.Registry
	user     *auth.User // logged-in end-user with a grant on siteA only
	admin    *auth.User // operator for DCR
	siteA    uint32
	siteB    uint32
	audience string
	issuer   string
}

// fakeSites is a static auth.SitesStore for the consent intersection.
type fakeSites struct {
	grants map[uuid.UUID]map[uint32]auth.Role
}

func (f fakeSites) LoadUserSites(_ context.Context, uid uuid.UUID) (map[uint32]auth.Role, error) {
	if g, ok := f.grants[uid]; ok {
		return g, nil
	}

	return map[uint32]auth.Role{}, nil
}
func (fakeSites) Grant(context.Context, uuid.UUID, uint32, auth.Role) error { return nil }
func (fakeSites) Revoke(context.Context, uuid.UUID, uint32) error           { return nil }
func (fakeSites) ListUsersBySite(context.Context, uint32) ([]auth.UserSiteGrant, error) {
	return nil, nil
}

func writeTestKey(t *testing.T) string {
	t.Helper()

	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}

	path := filepath.Join(t.TempDir(), "as.pem")
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}

	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	return path
}

func newASFixture(t *testing.T) *asFixture {
	t.Helper()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs: []string{clickhouseAddr()}, Database: testDatabase, Username: "default",
	}, logger)
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	migrator := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{Database: testDatabase}, logger)
	if migErr := migrator.Run(ctx); migErr != nil {
		t.Fatalf("migrate: %v", migErr)
	}

	auditLog, err := audit.New(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	t.Cleanup(func() { _ = auditLog.Close() })

	key, err := oauthas.LoadSigningKey(writeTestKey(t), nil, false)
	if err != nil {
		t.Fatalf("signing key: %v", err)
	}

	siteA, siteB := uint32(7001), uint32(7002)
	userID := uuid.New()

	user := &auth.User{UserID: userID, Role: auth.RoleViewer}
	admin := &auth.User{UserID: uuid.New(), Role: auth.RoleAdmin}

	sites := fakeSites{grants: map[uuid.UUID]map[uint32]auth.Role{
		userID: {siteA: auth.RoleViewer}, // user holds siteA only (NOT siteB)
	}}

	const issuer, audience = "https://app.statnive.live", "https://app.statnive.live/mcp"

	asStore := oauthas.NewStore(store.Conn(), testDatabase)
	reg := metrics.New()

	srv := oauthas.NewServer(oauthas.Config{
		Issuer:         issuer,
		Audience:       audience,
		Scope:          "analytics:read",
		AllowedSiteIDs: []uint32{siteA, siteB},
		CodeTTL:        60 * time.Second,
	}, asStore, key, sites, auditLog, reg, logger, time.Now)

	return &asFixture{
		srv: srv, store: asStore, key: key, reg: reg,
		user: user, admin: admin, siteA: siteA, siteB: siteB,
		audience: audience, issuer: issuer,
	}
}

// --- request helpers -------------------------------------------------------

func callForm(h http.HandlerFunc, method, target string, form url.Values, user *auth.User) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil && method == http.MethodPost {
		body = strings.NewReader(form.Encode())
	}

	req := httptest.NewRequest(method, target, body)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	if user != nil {
		req = req.WithContext(auth.WithSession(req.Context(), user, nil))
	}

	rec := httptest.NewRecorder()
	h(rec, req)

	return rec
}

func locParam(t *testing.T, rec *httptest.ResponseRecorder, key string) string {
	t.Helper()

	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location %q: %v", rec.Header().Get("Location"), err)
	}

	return u.Query().Get(key)
}

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// rawTok returns a unique raw credential for store-level tests.
func rawTok() string { return uuid.NewString() }

// registerClient runs DCR as the operator and returns (clientID, secret).
func (f *asFixture) registerClient(t *testing.T, redirectURI string) (string, string) {
	t.Helper()

	bodyJSON, _ := json.Marshal(map[string]any{
		"client_name":   "ChatGPT Test",
		"redirect_uris": []string{redirectURI},
	})

	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithSession(req.Context(), f.admin, nil))

	rec := httptest.NewRecorder()
	f.srv.Register(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("register: status %d, body %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("register resp: %v", err)
	}

	return resp.ClientID, resp.ClientSecret
}

// authorizeParams builds the shared /authorize + /consent query/form values.
func (f *asFixture) authorizeParams(clientID, redirectURI, challenge string) url.Values {
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"analytics:read"},
		"state":                 {"xyz-state"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
}

// mintCode runs authorize→consent(approve) and returns the authorization code.
// consentSites are the site_ids the user "checks" (may differ from grants to
// test the clamp).
func (f *asFixture) mintCode(t *testing.T, clientID, redirectURI, verifier string, consentSites []uint32) string {
	t.Helper()

	params := f.authorizeParams(clientID, redirectURI, challengeFor(verifier))

	// GET /authorize → consent page (200).
	if rec := callForm(f.srv.Authorize, http.MethodGet, "/authorize?"+params.Encode(), nil, f.user); rec.Code != http.StatusOK {
		t.Fatalf("authorize: status %d, body %s", rec.Code, rec.Body.String())
	}

	// POST /consent approve.
	form := params
	form.Set("decision", "approve")
	form.Del("site_ids")

	for _, s := range consentSites {
		form.Add("site_ids", strconv.FormatUint(uint64(s), 10))
	}

	rec := callForm(f.srv.Consent, http.MethodPost, "/consent", form, f.user)
	if rec.Code != http.StatusFound {
		t.Fatalf("consent: status %d, body %s", rec.Code, rec.Body.String())
	}

	code := locParam(t, rec, "code")
	if code == "" {
		t.Fatalf("consent returned no code; Location=%s", rec.Header().Get("Location"))
	}

	return code
}

func (f *asFixture) exchange(t *testing.T, form url.Values) (*httptest.ResponseRecorder, tokenResp) {
	t.Helper()

	rec := callForm(f.srv.Token, http.MethodPost, "/token", form, nil)

	var tr tokenResp
	_ = json.Unmarshal(rec.Body.Bytes(), &tr)

	return rec, tr
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

func codeExchangeForm(clientID, secret, code, redirectURI, verifier string) url.Values {
	return url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"client_secret": {secret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
}

// jwtSiteIDs decodes the site_ids claim from an access token (no verification —
// the signature is checked separately).
func jwtSiteIDs(t *testing.T, token string) []uint32 {
	t.Helper()

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}

	var c struct {
		SiteIDs []uint32 `json:"site_ids"`
		Scope   string   `json:"scope"`
		Aud     string   `json:"aud"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}

	return c.SiteIDs
}

// --- happy path ------------------------------------------------------------

func TestOAuthAS_HappyPath(t *testing.T) {
	f := newASFixture(t)
	const redirectURI = "https://chatgpt.com/connector/cb"

	clientID, secret := f.registerClient(t, redirectURI)
	verifier := strings.Repeat("a", 64)
	code := f.mintCode(t, clientID, redirectURI, verifier, []uint32{f.siteA})

	rec, tr := f.exchange(t, codeExchangeForm(clientID, secret, code, redirectURI, verifier))
	if rec.Code != http.StatusOK {
		t.Fatalf("token: status %d, body %s", rec.Code, rec.Body.String())
	}

	if tr.AccessToken == "" || tr.RefreshToken == "" || tr.TokenType != "Bearer" {
		t.Fatalf("incomplete token response: %+v", tr)
	}

	if got := jwtSiteIDs(t, tr.AccessToken); len(got) != 1 || got[0] != f.siteA {
		t.Errorf("access token site_ids = %v, want [%d]", got, f.siteA)
	}

	// Observability wiring: consent-granted + token-issued counters bumped.
	if f.reg.OAuthAuthorizeFor(metrics.OAuthGranted) < 1 {
		t.Error("metric statnive_mcp_oauth_authorize_total{granted} not incremented")
	}

	if f.reg.OAuthTokenFor(metrics.OAuthIssued) < 1 {
		t.Error("metric statnive_mcp_oauth_token_total{issued} not incremented")
	}
}

// --- store invariants ------------------------------------------------------

func TestOAuthAS_StoreSingleUseAndExpiry(t *testing.T) {
	f := newASFixture(t)
	ctx := context.Background()

	raw := rawTok()
	ac := oauthas.AuthCode{
		RedirectURI:   "https://x/cb",
		CodeChallenge: challengeFor(strings.Repeat("a", 64)),
		ExpiresAt:     time.Now().Add(time.Minute),
	}
	ac.ClientID, ac.UserID, ac.Scope, ac.Audience, ac.SiteIDs = "c1", f.user.UserID, "analytics:read", f.audience, []uint32{f.siteA}

	if err := f.store.SaveAuthCode(ctx, oauthas.HashToken(raw), ac); err != nil {
		t.Fatalf("save code: %v", err)
	}

	if _, err := f.store.ConsumeAuthCode(ctx, oauthas.HashToken(raw), time.Now()); err != nil {
		t.Fatalf("first consume: %v", err)
	}

	if _, err := f.store.ConsumeAuthCode(ctx, oauthas.HashToken(raw), time.Now()); err != oauthas.ErrCodeConsumed {
		t.Errorf("replay consume err = %v, want ErrCodeConsumed", err)
	}

	// Expired code.
	raw2 := rawTok()
	ac.ExpiresAt = time.Now().Add(-time.Second)

	if err := f.store.SaveAuthCode(ctx, oauthas.HashToken(raw2), ac); err != nil {
		t.Fatalf("save expired: %v", err)
	}

	if _, err := f.store.ConsumeAuthCode(ctx, oauthas.HashToken(raw2), time.Now()); err != oauthas.ErrCodeExpired {
		t.Errorf("expired consume err = %v, want ErrCodeExpired", err)
	}
}

func TestOAuthAS_RefreshRotationAndReuse(t *testing.T) {
	f := newASFixture(t)
	ctx := context.Background()

	old := rawTok()
	rt := oauthas.RefreshToken{FamilyID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour)}
	rt.ClientID, rt.UserID, rt.Scope, rt.Audience, rt.SiteIDs = "c1", f.user.UserID, "analytics:read", f.audience, []uint32{f.siteA}

	if err := f.store.SaveRefreshToken(ctx, oauthas.HashToken(old), rt); err != nil {
		t.Fatalf("save refresh: %v", err)
	}

	next := rawTok()
	if _, err := f.store.RotateRefreshToken(ctx, oauthas.HashToken(old), oauthas.HashToken(next), "c1", time.Now()); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	// Reuse of the rotated (old) token → reuse detected.
	again := rawTok()
	if _, err := f.store.RotateRefreshToken(ctx, oauthas.HashToken(old), oauthas.HashToken(again), "c1", time.Now()); err != oauthas.ErrRefreshReused {
		t.Errorf("reuse-old err = %v, want ErrRefreshReused", err)
	}

	// The successor is now in a revoked family → also dead.
	if _, err := f.store.RotateRefreshToken(ctx, oauthas.HashToken(next), oauthas.HashToken(again), "c1", time.Now()); err != oauthas.ErrRefreshReused {
		t.Errorf("successor-after-family-revoke err = %v, want ErrRefreshReused", err)
	}

	// Wrong client presenting a fresh token → reuse/theft.
	fresh := rawTok()
	rt2 := rt
	rt2.FamilyID = uuid.New()

	if err := f.store.SaveRefreshToken(ctx, oauthas.HashToken(fresh), rt2); err != nil {
		t.Fatalf("save fresh: %v", err)
	}

	if _, err := f.store.RotateRefreshToken(ctx, oauthas.HashToken(fresh), oauthas.HashToken(again), "WRONG-CLIENT", time.Now()); err != oauthas.ErrRefreshReused {
		t.Errorf("wrong-client err = %v, want ErrRefreshReused", err)
	}
}

// --- J red-team ------------------------------------------------------------

func TestOAuthAS_RedTeam(t *testing.T) {
	f := newASFixture(t)
	const redirectURI = "https://chatgpt.com/cb"

	clientID, secret := f.registerClient(t, redirectURI)
	verifier := strings.Repeat("a", 64)

	t.Run("code replay rejected", func(t *testing.T) {
		code := f.mintCode(t, clientID, redirectURI, verifier, []uint32{f.siteA})

		if rec, _ := f.exchange(t, codeExchangeForm(clientID, secret, code, redirectURI, verifier)); rec.Code != http.StatusOK {
			t.Fatalf("first exchange status %d", rec.Code)
		}

		rec, tr := f.exchange(t, codeExchangeForm(clientID, secret, code, redirectURI, verifier))
		if rec.Code == http.StatusOK || tr.Error != "invalid_grant" {
			t.Errorf("replay: status %d err %q, want 400 invalid_grant", rec.Code, tr.Error)
		}
	})

	t.Run("PKCE strip rejected", func(t *testing.T) {
		code := f.mintCode(t, clientID, redirectURI, verifier, []uint32{f.siteA})

		rec, tr := f.exchange(t, codeExchangeForm(clientID, secret, code, redirectURI, "WRONG-verifier-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
		if rec.Code == http.StatusOK || tr.Error != "invalid_grant" {
			t.Errorf("pkce strip: status %d err %q, want invalid_grant", rec.Code, tr.Error)
		}
	})

	t.Run("cross-client code use rejected", func(t *testing.T) {
		client2, secret2 := f.registerClient(t, redirectURI)
		code := f.mintCode(t, clientID, redirectURI, verifier, []uint32{f.siteA})

		rec, tr := f.exchange(t, codeExchangeForm(client2, secret2, code, redirectURI, verifier))
		if rec.Code == http.StatusOK || tr.Error != "invalid_grant" {
			t.Errorf("cross-client: status %d err %q, want invalid_grant", rec.Code, tr.Error)
		}
	})

	t.Run("redirect smuggle rejected pre-redirect", func(t *testing.T) {
		params := f.authorizeParams(clientID, "https://evil.example.com/cb", challengeFor(verifier))

		rec := callForm(f.srv.Authorize, http.MethodGet, "/authorize?"+params.Encode(), nil, f.user)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("smuggle: status %d, want 400 (NO redirect)", rec.Code)
		}

		if rec.Header().Get("Location") != "" {
			t.Errorf("smuggle produced a redirect (open redirect): %s", rec.Header().Get("Location"))
		}
	})

	t.Run("PKCE downgrade rejected", func(t *testing.T) {
		params := f.authorizeParams(clientID, redirectURI, challengeFor(verifier))
		params.Set("code_challenge_method", "plain")

		rec := callForm(f.srv.Authorize, http.MethodGet, "/authorize?"+params.Encode(), nil, f.user)
		if locParam(t, rec, "error") != "invalid_request" {
			t.Errorf("downgrade error = %q, want invalid_request", locParam(t, rec, "error"))
		}
	})

	t.Run("scope escalation rejected", func(t *testing.T) {
		params := f.authorizeParams(clientID, redirectURI, challengeFor(verifier))
		params.Set("scope", "analytics:read admin:write")

		rec := callForm(f.srv.Authorize, http.MethodGet, "/authorize?"+params.Encode(), nil, f.user)
		if locParam(t, rec, "error") != "invalid_scope" {
			t.Errorf("scope escalation error = %q, want invalid_scope", locParam(t, rec, "error"))
		}
	})

	t.Run("consent site escalation clamped", func(t *testing.T) {
		// User holds siteA only; "checks" siteA + siteB. The issued code/token
		// must carry siteA only.
		code := f.mintCode(t, clientID, redirectURI, verifier, []uint32{f.siteA, f.siteB})

		_, tr := f.exchange(t, codeExchangeForm(clientID, secret, code, redirectURI, verifier))
		if got := jwtSiteIDs(t, tr.AccessToken); len(got) != 1 || got[0] != f.siteA {
			t.Errorf("escalation: token site_ids = %v, want [%d] only", got, f.siteA)
		}
	})

	t.Run("consent to only-unauthorized site denied", func(t *testing.T) {
		params := f.authorizeParams(clientID, redirectURI, challengeFor(verifier))
		params.Set("decision", "approve")
		params.Set("site_ids", strconv.FormatUint(uint64(f.siteB), 10)) // user lacks siteB

		rec := callForm(f.srv.Consent, http.MethodPost, "/consent", params, f.user)
		if locParam(t, rec, "error") != "access_denied" {
			t.Errorf("unauthorized-only consent error = %q, want access_denied", locParam(t, rec, "error"))
		}
	})

	t.Run("refresh reuse after rotate revokes family", func(t *testing.T) {
		code := f.mintCode(t, clientID, redirectURI, verifier, []uint32{f.siteA})

		_, tr := f.exchange(t, codeExchangeForm(clientID, secret, code, redirectURI, verifier))
		if tr.RefreshToken == "" {
			t.Fatal("no refresh token issued")
		}

		// Rotate once (ok).
		_, tr2 := f.exchange(t, url.Values{
			"grant_type": {"refresh_token"}, "client_id": {clientID},
			"client_secret": {secret}, "refresh_token": {tr.RefreshToken},
		})
		if tr2.AccessToken == "" {
			t.Fatalf("rotation failed: %+v", tr2)
		}

		// Reuse the ORIGINAL refresh → reuse detected.
		recReuse, trReuse := f.exchange(t, url.Values{
			"grant_type": {"refresh_token"}, "client_id": {clientID},
			"client_secret": {secret}, "refresh_token": {tr.RefreshToken},
		})
		if recReuse.Code == http.StatusOK || trReuse.Error != "invalid_grant" {
			t.Errorf("reuse: status %d err %q, want invalid_grant", recReuse.Code, trReuse.Error)
		}

		// The rotated successor is now dead too (family revoked).
		recSucc, _ := f.exchange(t, url.Values{
			"grant_type": {"refresh_token"}, "client_id": {clientID},
			"client_secret": {secret}, "refresh_token": {tr2.RefreshToken},
		})
		if recSucc.Code == http.StatusOK {
			t.Error("successor still valid after family revoke")
		}

		if f.reg.OAuthTokenFor(metrics.OAuthRefreshReuse) < 1 {
			t.Error("metric statnive_mcp_oauth_token_total{refresh_reuse} not incremented on reuse")
		}
	})

	t.Run("bad client secret rejected", func(t *testing.T) {
		code := f.mintCode(t, clientID, redirectURI, verifier, []uint32{f.siteA})

		rec, tr := f.exchange(t, codeExchangeForm(clientID, "WRONG-SECRET", code, redirectURI, verifier))
		if rec.Code != http.StatusUnauthorized || tr.Error != "invalid_client" {
			t.Errorf("bad secret: status %d err %q, want 401 invalid_client", rec.Code, tr.Error)
		}
	})
}
