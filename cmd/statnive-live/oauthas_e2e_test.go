//go:build integration && chatgpt_app

// Full-chain e2e (PR-E): an AS-shaped access token (RS256 JWT carrying the
// site_ids consent claim, signed by the AS key whose JWKS the RS fetches) flows
// through the REAL resource-server middleware (oauthMiddleware) into the real
// MCP server and reads live ClickHouse via the overview tool. Proves the AS↔RS
// contract end-to-end incl. M1: the token reads only its consented site, and a
// cross-tenant read is denied. Run: go test -tags 'integration chatgpt_app' ./cmd/...
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/mcp"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
	"github.com/statnive/statnive.live/internal/storage/storagetest"
)

func e2eCHAddr() string {
	if v := os.Getenv("STATNIVE_CLICKHOUSE_ADDR"); v != "" {
		return v
	}

	return "127.0.0.1:19000"
}

func TestOAuthAS_E2E_TokenReadsScopedSite(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs: []string{e2eCHAddr()}, Database: "statnive", Username: "default",
	}, logger)
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	defer func() { _ = store.Close() }()

	if mErr := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{Database: "statnive"}, logger).Run(ctx); mErr != nil {
		t.Fatalf("migrate: %v", mErr)
	}

	// Unique sites so the CH-oracle count is exact across reruns.
	base := uint32(time.Now().UnixNano()%1_000_000) + 81_000_000
	siteA, siteB := base, base+1

	storagetest.SeedSite(t, ctx, store.Conn(), siteA, fmt.Sprintf("a-%d.example.com", siteA))
	storagetest.SeedSite(t, ctx, store.Conn(), siteB, fmt.Sprintf("b-%d.example.com", siteB))

	const nA = 5

	var vh [16]byte
	copy(vh[:], "oauth-e2e-vhash1")

	events := make([]storagetest.SeedEvent, 0, nA)
	for i := range nA {
		events = append(events, storagetest.SeedEvent{
			SiteID: siteA, Time: time.Now().Add(-time.Hour), Pathname: fmt.Sprintf("/p-%d", i), VisitorHash: vh,
		})
	}

	storagetest.WriteEvents(t, ctx, store.Conn(), events)

	// Real MCP server.
	mcpSrv := mcp.New(mcp.Config{
		Store:      storage.NewCachedStore(storage.NewClickhouseQueryStore(store), 256),
		Concrete:   store,
		Health:     store.Conn(),
		Registry:   sites.New(store.Conn()),
		Log:        logger,
		Version:    "oauth-e2e",
		GeoEnabled: true,
		Budget: mcp.BudgetConfig{
			CallsPerMin: 1000, RowsPerMin: 1_000_000, CallsPerSession: 10_000,
			RowsPerSession: 10_000_000, DistinctSitesPerMin: 100, WildcardFactor: 1.0,
		},
		Now: time.Now,
	})

	// AS signing key → JWKS endpoint the RS verifier fetches.
	key := newTestKey(t)

	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwksJSON(&key.PublicKey, testKID))
	}))
	defer jwks.Close()

	const issuer, audience = "https://app.statnive.live", "https://app.statnive.live/mcp"

	mw, err := oauthMiddleware(mcpOAuthConfig{
		Enabled: true, Issuer: issuer, Audience: audience,
		JWKSURL: jwks.URL, AllowedSiteIDs: []uint32{siteA, siteB},
	}, logger)
	if err != nil {
		t.Fatalf("oauthMiddleware: %v", err)
	}

	httpSrv := httptest.NewServer(mw(mcpSrv.HTTPHandler(mcp.HTTPOptions{})))
	defer httpSrv.Close()

	// AS-shaped token: consented to siteA ONLY (the site_ids claim).
	claims := goodClaims(time.Now())
	claims["iss"] = issuer
	claims["aud"] = audience
	claims["scope"] = "analytics:read"
	claims["site_ids"] = []uint32{siteA}
	token := mintRS256(t, key, testKID, claims)

	overview := func(site uint32) string {
		return fmt.Sprintf(
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"overview","arguments":{"site":"%d","range":"7d"}}}`, site)
	}

	// Consented site → 200 + CH-oracle parity.
	code, resp := postMCPe2e(t, httpSrv.URL, overview(siteA), token)
	if code != http.StatusOK {
		t.Fatalf("siteA status = %d, want 200; resp=%v", code, resp)
	}

	result, _ := resp["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("siteA overview returned isError: %v", result)
	}

	sc, _ := result["structuredContent"].(map[string]any)
	if got := uint(asFloat(sc["pageviews"])); got != nA {
		t.Errorf("siteA pageviews = %d, want %d (CH-oracle)", got, nA)
	}

	// Cross-tenant: the siteA-scoped token must NOT read siteB (M1).
	_, respB := postMCPe2e(t, httpSrv.URL, overview(siteB), token)
	resB, _ := respB["result"].(map[string]any)
	_, hasErr := respB["error"]
	isErrB, _ := resB["isError"].(bool)

	if !hasErr && !isErrB {
		t.Errorf("cross-tenant read of siteB was NOT denied: %v", respB)
	}

	// No token → 401.
	if c, _ := postMCPe2e(t, httpSrv.URL, overview(siteA), ""); c != http.StatusUnauthorized {
		t.Errorf("no-token status = %d, want 401", c)
	}
}

func postMCPe2e(t *testing.T, url, body, bearer string) (int, map[string]any) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("req: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")

	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)

	var parsed map[string]any
	_ = json.Unmarshal(raw, &parsed)

	return resp.StatusCode, parsed
}

func asFloat(v any) float64 {
	f, _ := v.(float64)

	return f
}
