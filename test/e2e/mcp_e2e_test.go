//go:build e2e

// MCP end-to-end: drive the REAL compiled binary (`statnive-live mcp serve`)
// as a subprocess over BOTH transports against a docker ClickHouse, with a
// thin hand-rolled JSON-RPC client (no third-party MCP SDK). Unlike the
// in-process integration tests, this exercises the actual cmd/main.go
// subcommand dispatch, the reduced boot, flag parsing, and — for HTTP — the
// full auth/rate-limit middleware chain + bind guard.
//
// Requires `make ch-up` and a built binary. Run via `make mcp-e2e`, which
// builds the binary and sets STATNIVE_E2E_BIN.
package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/statnive/statnive.live/internal/storage"
	"github.com/statnive/statnive.live/internal/storage/storagetest"
)

const (
	e2eDatabase  = "statnive"
	e2eBearer    = "e2e-mcp-bearer-token-32bytes-minimum-xx"
	e2eProtoVers = "2025-06-18"
)

func e2eCHAddr() string {
	if v := os.Getenv("STATNIVE_CLICKHOUSE_ADDR"); v != "" {
		return v
	}

	return "127.0.0.1:19000"
}

func e2eBinary(t *testing.T) string {
	t.Helper()

	if v := os.Getenv("STATNIVE_E2E_BIN"); v != "" {
		return v
	}

	// Default: repo-root/bin/statnive-live (test runs from test/e2e/).
	abs, _ := filepath.Abs("../../bin/statnive-live")
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("binary not found at %s (run `make build` or `make mcp-e2e`): %v", abs, err)
	}

	return abs
}

// e2eSeed migrates the schema, seeds a fresh site + N pageviews, and returns
// the site_id + the CH-oracle pageview count from the rollup.
func e2eSeed(t *testing.T, n int) (uint32, uint64) {
	t.Helper()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs: []string{e2eCHAddr()}, Database: e2eDatabase, Username: "default",
	}, logger)
	if err != nil {
		t.Skipf("clickhouse unreachable (run `make ch-up`): %v", err)
	}

	t.Cleanup(func() { _ = store.Close() })

	if err := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{Database: e2eDatabase}, logger).Run(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Fresh, never-reused site_id so the rollup never accumulates across runs.
	siteID := uint32(8_000_000 + (time.Now().UnixNano()/1_000_000)%500_000)
	host := fmt.Sprintf("mcp-e2e-%d.example.com", siteID)

	// storagetest.SeedSite cleans by site_id OR hostname (multitenant-flake
	// safe); WriteEvents batch-inserts then OPTIMIZE ... FINALs the rollups so
	// the aggregated state is immediately query-able (no MV-lag poll needed).
	storagetest.SeedSite(t, ctx, store.Conn(), siteID, host)

	now := time.Now()
	events := make([]storagetest.SeedEvent, n)

	for i := range events {
		events[i] = storagetest.SeedEvent{
			SiteID:      siteID,
			Time:        now,
			Pathname:    fmt.Sprintf("/p-%d", i),
			VisitorHash: [16]byte{0x0a},
		}
	}

	storagetest.WriteEvents(t, ctx, store.Conn(), events)

	var rollupPV uint64
	if err := store.Conn().QueryRow(ctx,
		`SELECT sum(pageviews) FROM statnive.hourly_visitors WHERE site_id = ?`, siteID,
	).Scan(&rollupPV); err != nil {
		t.Fatalf("rollup oracle: %v", err)
	}

	if rollupPV != uint64(n) {
		t.Fatalf("rollup oracle = %d, want %d", rollupPV, n)
	}

	return siteID, rollupPV
}

// e2eConfig writes a minimal config the mcp subcommand can boot from. An empty
// httpListen means stdio-only (HTTP transport disabled); a non-empty value
// enables the HTTP transport on that address.
func e2eConfig(t *testing.T, httpListen string) string {
	t.Helper()

	dir := t.TempDir()

	cfg := fmt.Sprintf(`clickhouse:
  addr: %q
  database: %q
  username: "default"
audit:
  path: %q
alerts:
  sink_path: %q
dashboard:
  geo_enabled: true
  bearer_token: %q
mcp:
  http:
    enabled: %t
    listen: %q
  budget:
    calls_per_min: 100000
    rows_per_min: 100000000
    calls_per_session: 1000000
    rows_per_session: 1000000000
    distinct_sites_per_min: 100000
    wildcard_tier_factor: 1.0
`, e2eCHAddr(), e2eDatabase,
		filepath.Join(dir, "audit.jsonl"),
		filepath.Join(dir, "alerts.jsonl"),
		e2eBearer, httpListen != "", httpListen)

	path := filepath.Join(dir, "mcp-e2e-config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func structuredPageviews(t *testing.T, result map[string]any) uint64 {
	t.Helper()

	sc, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("no structuredContent: %v", result)
	}

	pv, ok := sc["pageviews"].(float64)
	if !ok {
		t.Fatalf("pageviews not a number: %v", sc["pageviews"])
	}

	return uint64(pv)
}

func TestMCPe2e_Stdio_RealBinary_CHOracle(t *testing.T) {
	bin := e2eBinary(t)
	siteID, wantPV := e2eSeed(t, 9)
	cfgPath := e2eConfig(t, "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "mcp", "serve", "--transport=stdio", "--all-sites", "-c", cfgPath)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}

	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"overview","arguments":{"site":"%d","range":"30d"}}}`, siteID),
	}, "\n") + "\n"

	if _, err := io.WriteString(stdin, reqs); err != nil {
		t.Fatalf("write reqs: %v", err)
	}

	_ = stdin.Close()

	byID := map[float64]map[string]any{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var resp map[string]any
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("decode response %q: %v", line, err)
		}

		if id, ok := resp["id"].(float64); ok {
			byID[id] = resp
		}
	}

	_ = cmd.Wait()

	// initialize
	initRes, _ := byID[1]["result"].(map[string]any)
	if initRes == nil || initRes["protocolVersion"] == nil {
		t.Fatalf("initialize failed: %v", byID[1])
	}

	// tools/list = 19
	listRes, _ := byID[2]["result"].(map[string]any)
	tools, _ := listRes["tools"].([]any)

	if len(tools) != 19 {
		t.Errorf("tools/list = %d tools, want 19", len(tools))
	}

	// overview CH-oracle parity through the real binary.
	ovRes, _ := byID[3]["result"].(map[string]any)
	if ovRes == nil {
		t.Fatalf("overview missing: %v", byID[3])
	}

	if got := structuredPageviews(t, ovRes); got != wantPV {
		t.Errorf("e2e overview pageviews = %d, want %d (CH-oracle)", got, wantPV)
	}
}

func TestMCPe2e_HTTP_RealBinary_AuthAndOracle(t *testing.T) {
	bin := e2eBinary(t)
	siteID, wantPV := e2eSeed(t, 7)

	// Grab a free loopback port, then let the server bind it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}

	addr := ln.Addr().String()
	_ = ln.Close()

	cfgPath := e2eConfig(t, addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "mcp", "serve", "--transport=http", "-c", cfgPath)
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}

	t.Cleanup(func() { cancel(); _ = cmd.Wait() })

	// Wait for the listener.
	url := "http://" + addr + "/mcp"
	waitForHTTP(t, addr, 10*time.Second)

	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"overview","arguments":{"site":"%d","range":"30d"}}}`, siteID)

	// 1. Authorized → 200 + CH-oracle parity.
	resp := e2ePost(t, url, body, e2eBearer)
	if resp.status != http.StatusOK {
		t.Fatalf("authorized POST status = %d, want 200; body=%s", resp.status, resp.bodyText)
	}

	var ok map[string]any
	if err := json.Unmarshal([]byte(resp.bodyText), &ok); err != nil {
		t.Fatalf("decode 200 body: %v", err)
	}

	okRes, _ := ok["result"].(map[string]any)
	if okRes == nil {
		t.Fatalf("no result in 200 body: %s", resp.bodyText)
	}

	if got := structuredPageviews(t, okRes); got != wantPV {
		t.Errorf("e2e HTTP overview pageviews = %d, want %d (CH-oracle)", got, wantPV)
	}

	// 2. Unauthorized (no Bearer) → 401, never data.
	noauth := e2ePost(t, url, body, "")
	if noauth.status != http.StatusUnauthorized {
		t.Errorf("unauthenticated POST status = %d, want 401; body=%s", noauth.status, noauth.bodyText)
	}
}

type e2eResp struct {
	status   int
	bodyText string
}

func e2ePost(t *testing.T, url, body, bearer string) e2eResp {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("MCP-Protocol-Version", e2eProtoVers)

	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	b, _ := io.ReadAll(resp.Body)

	return e2eResp{status: resp.StatusCode, bodyText: string(bytes.TrimSpace(b))}
}

func waitForHTTP(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = c.Close()

			return
		}

		time.Sleep(150 * time.Millisecond)
	}

	t.Fatalf("mcp http server did not come up on %s within %s", addr, timeout)
}

// TestMain skips the whole suite if ClickHouse is unreachable.
func TestMain(m *testing.M) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{e2eCHAddr()},
		Auth: clickhouse.Auth{Database: e2eDatabase, Username: "default"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcp-e2e: clickhouse open failed, skipping:", err)
		os.Exit(0)
	}

	pctx, pcancel := context.WithTimeout(context.Background(), 3*time.Second)
	pingErr := conn.Ping(pctx)

	pcancel()
	_ = conn.Close()

	if pingErr != nil {
		fmt.Fprintln(os.Stderr, "mcp-e2e: clickhouse ping failed, skipping:", pingErr)
		os.Exit(0)
	}

	os.Exit(m.Run())
}
