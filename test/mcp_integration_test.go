//go:build integration

// MCP server integration tests — drive the real read-only MCP server over the
// stdio JSON-RPC framing against a real ClickHouse (the rollups populated by
// the ingest pipeline), and assert two oracles:
//
//   - Ground truth: the MCP overview's pageviews/visitors match the raw
//     events_raw counts the pipeline produced.
//   - No parallel data path: the MCP tool output equals a direct
//     storage.CachedStore.Overview call on the same filter — proving the
//     marshal/sanitize choke point preserves values byte-for-byte.
//
// Plus cross-tenant isolation through the full server path. Requires
// `make ch-up`; run via `make test-integration`.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/mcp"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
	"github.com/statnive/statnive.live/internal/timewindow"
)

// mcpTestServer builds an MCP server on top of the integration stack's real
// CH connection (cached store + registry), with generous budgets so the test
// itself never trips a cap.
func mcpTestServer(t *testing.T, store *storage.ClickHouseStore) *mcp.Server {
	t.Helper()

	cached := storage.NewCachedStore(storage.NewClickhouseQueryStore(store), 256)

	return mcp.New(mcp.Config{
		Store:      cached,
		Concrete:   store,        // off-interface event_audit read
		Health:     store.Conn(), // CH liveness for system_health
		Registry:   sites.New(store.Conn()),
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version:    "itest",
		GeoEnabled: true, // advertise the full catalog in tools/list
		Budget: mcp.BudgetConfig{
			CallsPerMin: 1000, RowsPerMin: 1_000_000, CallsPerSession: 10_000,
			RowsPerSession: 10_000_000, DistinctSitesPerMin: 100, WildcardFactor: 1.0,
		},
		Now: time.Now,
	})
}

// ingestPageviews posts n pageviews through the stack's /api/event endpoint
// and blocks until they land in events_raw.
func ingestPageviews(t *testing.T, stack *integrationStack, hostname string, siteID uint32, n int) {
	t.Helper()

	client := &http.Client{Timeout: testHTTPTimeout}

	for i := 0; i < n; i++ {
		body, _ := json.Marshal(ingest.RawEvent{
			Hostname:  hostname,
			Pathname:  fmt.Sprintf("/p-%03d", i),
			EventType: "pageview",
			EventName: "pageview",
		})

		req, err := http.NewRequestWithContext(stack.ctx, http.MethodPost, stack.srv.URL+"/api/event", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("event %d req: %v", i, err)
		}

		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("User-Agent", "Mozilla/5.0 (MCPIntegration/1.0) BrowserLike")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("event %d status = %d, want 202", i, resp.StatusCode)
		}
	}

	waitForCount(t, stack.ctx, stack.store, siteID, n, flushTimeout)
}

// callTool runs initialize + one tools/call (or tools/list) over the real
// stdio framing and returns the parsed JSON-RPC response for id 2.
func callTool(t *testing.T, srv *mcp.Server, allowAll bool, allowSites []uint32, toolJSON string) map[string]any {
	t.Helper()

	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		toolJSON,
	}, "\n")

	var out bytes.Buffer

	actor := mcp.StdioActor(allowSites, allowAll)
	if err := srv.ServeStdio(context.Background(), strings.NewReader(in), &out, actor); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}

	// The second non-empty line is the tools/call response (id 2).
	var lines []string

	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}

	if len(lines) < 2 {
		t.Fatalf("expected >=2 response lines, got %d:\n%s", len(lines), out.String())
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &resp); err != nil {
		t.Fatalf("decode tools/call response: %v", err)
	}

	return resp
}

func TestMCP_Overview_CHOracleParity(t *testing.T) {
	const nEvents = 12

	siteID, hostname := uniqueSite()

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	ingestPageviews(t, stack, hostname, siteID, nEvents)

	srv := mcpTestServer(t, stack.store)

	resp := callTool(t, srv, true, nil, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"overview","arguments":{"site":"%d","range":"7d"}}}`,
		siteID))

	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("no result in response: %v", resp)
	}

	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("overview returned isError: %v", result)
	}

	sc, _ := result["structuredContent"].(map[string]any)
	if sc == nil {
		t.Fatalf("no structuredContent: %v", result)
	}

	mcpPageviews := toUint(t, sc["pageviews"])
	mcpVisitors := toUint(t, sc["visitors"])

	// Oracle 1 — ground truth: pageviews == events ingested; visitors == 1
	// (all events share IP+UA+day salt → one visitor hash; uniqCombined64 is
	// exact at this cardinality).
	if mcpPageviews != nEvents {
		t.Errorf("MCP overview pageviews = %d, want %d (raw events ingested)", mcpPageviews, nEvents)
	}

	if mcpVisitors != 1 {
		t.Errorf("MCP overview visitors = %d, want 1", mcpVisitors)
	}

	// Oracle 2 — no parallel data path: MCP output == a direct CachedStore
	// call on the identical filter. Proves marshalResult's sanitize round-trip
	// preserves the numbers.
	from, to, err := timewindow.ParseRange("7d", time.UTC, time.Now())
	if err != nil {
		t.Fatalf("ParseRange: %v", err)
	}

	direct, err := storage.NewCachedStore(storage.NewClickhouseQueryStore(stack.store), 16).
		Overview(stack.ctx, &storage.Filter{SiteID: siteID, From: from, To: to})
	if err != nil {
		t.Fatalf("direct Overview: %v", err)
	}

	if mcpPageviews != direct.Pageviews || mcpVisitors != direct.Visitors {
		t.Errorf("MCP != direct Store: mcp(pv=%d,v=%d) direct(pv=%d,v=%d)",
			mcpPageviews, mcpVisitors, direct.Pageviews, direct.Visitors)
	}
}

func TestMCP_CrossTenant_RealCH(t *testing.T) {
	siteID, hostname := uniqueSite()

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	// No ingest needed: the cross-tenant denial happens at ActorCanReadSite,
	// before any query. The site only needs to exist (seeded by the stack).
	srv := mcpTestServer(t, stack.store)

	// Actor scoped to a DIFFERENT site → reading siteID must be -32602, never
	// an empty result.
	resp := callTool(t, srv, false, []uint32{9999}, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"overview","arguments":{"site":"%d","range":"7d"}}}`,
		siteID))

	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("cross-tenant call should be a JSON-RPC error, got: %v", resp)
	}

	if code := toInt(t, errObj["code"]); code != -32602 {
		t.Errorf("cross-tenant error code = %d, want -32602", code)
	}

	if _, ok := resp["result"]; ok {
		t.Error("cross-tenant denial must not carry a result")
	}
}

func TestMCP_EventAudit_RealCH(t *testing.T) {
	siteID, hostname := uniqueSite()

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	// Ingest a few pageviews → one distinct event_name ("pageview") in
	// events_raw → event_audit reports it via the off-interface
	// EventNameCardinality read, under the CNIL ceiling → cap_status "ok".
	ingestPageviews(t, stack, hostname, siteID, 4)

	srv := mcpTestServer(t, stack.store)

	// allowAll=true → an admin-role wildcard actor (event_audit is admin-only).
	resp := callTool(t, srv, true, nil, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"event_audit","arguments":{"site":"%d","range":"30d"}}}`,
		siteID))

	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("no result: %v", resp)
	}

	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("event_audit returned isError: %v", result)
	}

	sc, _ := result["structuredContent"].(map[string]any)
	if toUint(t, sc["distinct"]) < 1 {
		t.Errorf("event_audit distinct = %v, want >=1", sc["distinct"])
	}

	if sc["cap_status"] != "ok" {
		t.Errorf("event_audit cap_status = %v, want ok (1 event name under the 3-ceiling)", sc["cap_status"])
	}
}

func TestMCP_EventAudit_DeniedForAPIRole_RealCH(t *testing.T) {
	siteID, hostname := uniqueSite()

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	srv := mcpTestServer(t, stack.store)

	// allowAll=false + allow-sites=[siteID] → a SCOPED wildcard? No: the stdio
	// scoped operator is admin-role, so it would pass. To exercise the api-role
	// denial we'd need an api token — that's covered exhaustively in the unit
	// suite (TestAdminTools_DeniedForAPIRole). Here we just confirm a scoped
	// admin operator that lacks the site is denied at the tenancy gate.
	resp := callTool(t, srv, false, []uint32{999999}, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"event_audit","arguments":{"site":"%d","range":"7d"}}}`,
		siteID))

	if errObj, _ := resp["error"].(map[string]any); errObj == nil || toInt(t, errObj["code"]) != -32602 {
		t.Errorf("event_audit cross-tenant should be -32602, got %v", resp)
	}
}

func TestMCP_Stdio_ToolsList_RealCH(t *testing.T) {
	siteID, hostname := uniqueSite()

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	srv := mcpTestServer(t, stack.store)

	resp := callTool(t, srv, true, nil, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)

	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)

	// 17 published = catalog (19, geo enabled) minus the 2 reserved tools
	// (devices, funnel) gated out of tools/list until their backing
	// rollup/query ships (ChatGPT discovery-precision; toolDef.Reserved).
	if len(tools) != 17 {
		t.Fatalf("tools/list returned %d tools, want 17 (catalog 19 minus reserved devices+funnel, geo enabled)", len(tools))
	}
}

func TestMCP_SystemHealth_RealCH(t *testing.T) {
	siteID, hostname := uniqueSite()

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	srv := mcpTestServer(t, stack.store)

	// allowAll=true → admin actor (system_health is admin-only). Real CH is up.
	resp := callTool(t, srv, true, nil,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"system_health","arguments":{}}}`)

	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("no result: %v", resp)
	}

	sc, _ := result["structuredContent"].(map[string]any)
	if sc["clickhouse"] != "up" {
		t.Errorf("system_health clickhouse = %v, want up (real CH reachable)", sc["clickhouse"])
	}
}

// mcpSiteBase is per-test-invocation (millisecond-derived) so rollup state
// from a prior `go test` run never accumulates into this run — overview reads
// the AggregatingMergeTree rollup, whose state rows survive an events_raw
// delete. Combined with mcpSiteSeq it yields a fresh, never-before-used
// site_id per test, so no rollup clean-up mutations are needed (keeping the
// suite's CH mutation load low).
var mcpSiteBase = 7_000_000 + uint32((time.Now().UnixNano()/1_000_000)%500_000)

var mcpSiteSeq atomic.Uint32

// uniqueSite returns a fresh (site_id, hostname) for one MCP integration test.
func uniqueSite() (uint32, string) {
	id := mcpSiteBase + mcpSiteSeq.Add(1)

	return id, fmt.Sprintf("mcp-itest-%d.example.com", id)
}

func toUint(t *testing.T, v any) uint64 {
	t.Helper()

	f, ok := v.(float64)
	if !ok {
		t.Fatalf("value %v (%T) is not a JSON number", v, v)
	}

	return uint64(f)
}

func toInt(t *testing.T, v any) int {
	t.Helper()

	f, ok := v.(float64)
	if !ok {
		t.Fatalf("value %v (%T) is not a JSON number", v, v)
	}

	return int(f)
}
