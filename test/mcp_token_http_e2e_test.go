//go:build integration

// PR-D bridge e2e: a dashboard-minted token must authenticate AND stay
// site-scoped on the real /mcp HTTP path (the exact auth chain serveMCPHTTP
// builds: APITokenMiddleware{DynamicTokens} → RequireAuthenticated →
// srv.HTTPHandler), and a revoke must take effect immediately (401).
// Proves the WS5 bridge end-to-end against real ClickHouse. Run: make test-integration
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/mcp"
	"github.com/statnive/statnive.live/internal/storage/storagetest"
)

func postMCP(t *testing.T, url, body, bearer string) (int, map[string]any) {
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

func TestMCPToken_HTTPBridge_AuthScopeRevoke(t *testing.T) {
	ctx := context.Background()
	_, conn := migratedTokenStore(t) // PR-A helper: migrates + returns (*ClickHouseAPITokenStore, *ClickHouseStore)

	// Two sites; the token is scoped to siteA only.
	siteA, hostA := uniqueSite()
	siteB, hostB := uniqueSite()
	storagetest.SeedSite(t, ctx, conn.Conn(), siteA, hostA)
	storagetest.SeedSite(t, ctx, conn.Conn(), siteB, hostB)

	const nA = 7

	var vh [16]byte
	copy(vh[:], "tokbridge-vhash1")

	events := make([]storagetest.SeedEvent, 0, nA)
	for i := range nA {
		events = append(events, storagetest.SeedEvent{
			SiteID: siteA, Time: time.Now().Add(-time.Hour), Pathname: fmt.Sprintf("/p-%d", i), VisitorHash: vh,
		})
	}

	storagetest.WriteEvents(t, ctx, conn.Conn(), events)

	// Production auth chain over the MCP HTTP handler, with the dynamic-token
	// store (the bridge). Mirrors serveMCPHTTP's composition exactly.
	tokenStore := auth.NewCachedAPITokenStore(auth.NewClickHouseAPITokenStore(conn.Conn(), testDatabase), 0)
	deps := auth.MiddlewareDeps{DynamicTokens: tokenStore}
	srv := mcpTestServer(t, conn)
	chain := auth.APITokenMiddleware(deps)(auth.RequireAuthenticated(nil)(srv.HTTPHandler(mcp.HTTPOptions{})))

	httpSrv := httptest.NewServer(chain)
	defer httpSrv.Close()

	// Mint a dashboard-style token scoped to siteA.
	raw, meta, err := tokenStore.Create(ctx, uuid.New(), "e2e-laptop", []uint32{siteA}, auth.RoleAPI, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	overview := func(site uint32) string {
		return fmt.Sprintf(
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"overview","arguments":{"site":"%d","range":"7d"}}}`,
			site)
	}

	// 1. No token → 401.
	if code, _ := postMCP(t, httpSrv.URL, overview(siteA), ""); code != http.StatusUnauthorized {
		t.Errorf("no-token status = %d, want 401", code)
	}

	// 2. Minted token on its own site → 200 + CH-oracle parity.
	code, resp := postMCP(t, httpSrv.URL, overview(siteA), raw)
	if code != http.StatusOK {
		t.Fatalf("authed status = %d, want 200; resp=%v", code, resp)
	}

	result, _ := resp["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("overview on own site returned isError: %v", result)
	}

	sc, _ := result["structuredContent"].(map[string]any)
	if got := toUint(t, sc["pageviews"]); got != nA {
		t.Errorf("overview pageviews = %d, want %d (CH-oracle)", got, nA)
	}

	// 3. Cross-tenant: the siteA token must NOT read siteB → tool error (-32602).
	_, respB := postMCP(t, httpSrv.URL, overview(siteB), raw)
	resB, _ := respB["result"].(map[string]any)
	_, hasErr := respB["error"]
	isErrB, _ := resB["isError"].(bool)
	if !hasErr && !isErrB {
		t.Errorf("cross-tenant read was not denied: %v", respB)
	}

	// 4. Revoke → next call 401 (immediate-revoke via cache flush).
	if err := tokenStore.Revoke(ctx, meta.TokenID, meta.UserID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if code, _ := postMCP(t, httpSrv.URL, overview(siteA), raw); code != http.StatusUnauthorized {
		t.Errorf("post-revoke status = %d, want 401", code)
	}
}
