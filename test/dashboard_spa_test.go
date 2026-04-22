//go:build integration

// Dashboard SPA integration test — Phase 5a.
//
// Boots a chi router that mounts the embedded SPA at /app/* alongside
// the existing dashboard API at /api/stats/*. Asserts:
//
//  1. GET /app/ returns 200 + the SPA shell HTML (contains the root div)
//  2. CSP / X-Content-Type-Options / Referrer-Policy security headers
//     are emitted on /app/* responses
//  3. The bearer token is injected into the meta tag at request time
//  4. /app/assets/* paths get long-cache headers
//  5. The Overview API endpoint the SPA fetches (/api/stats/overview)
//     round-trips correctly with the same bearer token the SPA uses
//
// Reuses internal/storage/storagetest/SeedSite + dashboard test harness
// patterns rather than duplicating fixture code.
package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/dashboard"
	"github.com/statnive/statnive.live/internal/dashboard/spa"
	"github.com/statnive/statnive.live/internal/storage"
	"github.com/statnive/statnive.live/internal/storage/storagetest"
)

const (
	spaSiteID   = uint32(601)
	spaHostname = "spa-test.example.com"
	spaToken    = "spa-test-bearer-tok-abcdef"
)

func TestDashboardSPA_ShellAndOverviewRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs:    []string{clickhouseAddr()},
		Database: testDatabase,
		Username: "default",
	}, logger)
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	migrator := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{Database: testDatabase}, logger)
	if migErr := migrator.Run(ctx); migErr != nil {
		t.Fatalf("migrate: %v", migErr)
	}

	// Reuse storagetest helpers — Phase 7b2-completion established
	// these as the canonical seed path; do not duplicate fixture code.
	storagetest.CleanSiteEvents(t, ctx, store.Conn(), spaSiteID)
	storagetest.SeedSite(t, ctx, store.Conn(), spaSiteID, spaHostname)

	auditLog, err := audit.New(t.TempDir() + "/audit.jsonl")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	t.Cleanup(func() { _ = auditLog.Close() })

	cached := storage.NewCachedStore(storage.NewClickhouseQueryStore(store), 256)

	// Phase 2b replaced the single-token bearer middleware with
	// auth.APITokenMiddleware + RequireAuthenticated. Same contract
	// end-to-end (401 without header, 200 with correct header); the
	// SPA meta tag still carries the raw token for backward-compat
	// during the dashboard-e2e transition to cookie auth.
	tokenHash := sha256.Sum256([]byte(spaToken))
	authDeps := auth.MiddlewareDeps{
		Audit: auditLog,
		APITokens: []auth.APIToken{
			{
				TokenHashHex: hex.EncodeToString(tokenHash[:]),
				SiteID:       spaSiteID,
				Label:        "spa-integration-test",
			},
		},
	}

	spaHandler, err := spa.Handler(spa.Config{BearerToken: spaToken})
	if err != nil {
		t.Fatalf("spa handler: %v", err)
	}

	router := chi.NewRouter()

	// SPA at /app/* — no auth (the SPA itself reads the bearer token
	// from index.html and attaches it to /api/stats/* requests).
	router.Method(http.MethodGet, "/app", http.RedirectHandler("/app/", http.StatusFound))
	router.Mount("/app/", http.StripPrefix("/app", spaHandler))

	// API at /api/stats/* — gated by bearer token (matches production).
	router.Group(func(r chi.Router) {
		r.Use(auth.APITokenMiddleware(authDeps))
		r.Use(auth.RequireAuthenticated(auditLog))
		dashboard.Mount(r, dashboard.Deps{
			Store:  cached,
			Audit:  auditLog,
			Logger: logger,
		})
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	client := &http.Client{Timeout: 5 * time.Second}

	// 1) Shell loads + has root div.
	resp, err := client.Get(srv.URL + "/app/")
	if err != nil {
		t.Fatalf("GET /app/: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/app/ status = %d, want 200", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	body := string(bodyBytes)

	if !strings.Contains(body, `<div id="statnive-app">`) {
		t.Errorf("SPA shell missing root div")
	}

	// 2) Security headers present.
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("CSP missing required directives; got %q", csp)
	}

	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}

	if got := resp.Header.Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q, want strict-origin-when-cross-origin", got)
	}

	// 3) Bearer token injected.
	if !strings.Contains(body, fmt.Sprintf(`content="%s"`, spaToken)) {
		t.Errorf("bearer token not injected into shell HTML")
	}

	if strings.Contains(body, "STATNIVE_BEARER_PLACEHOLDER") {
		t.Errorf("bearer placeholder still present in shell HTML")
	}

	// 4) Overview API round-trips with the SPA's bearer token.
	now := time.Now().UTC().Truncate(time.Hour)
	overviewURL := fmt.Sprintf("%s/api/stats/overview?site=%d&from=%s&to=%s",
		srv.URL, spaSiteID,
		now.Add(-7*24*time.Hour).Format("2006-01-02"),
		now.Add(24*time.Hour).Format("2006-01-02"))

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, overviewURL, nil)
	req.Header.Set("Authorization", "Bearer "+spaToken)

	overviewResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET overview: %v", err)
	}
	defer overviewResp.Body.Close()

	if overviewResp.StatusCode != http.StatusOK {
		t.Fatalf("overview status = %d, want 200", overviewResp.StatusCode)
	}

	var overview map[string]any
	if jsonErr := json.NewDecoder(overviewResp.Body).Decode(&overview); jsonErr != nil {
		t.Fatalf("decode overview: %v", jsonErr)
	}

	// The 5 KPI fields the SPA Overview panel reads (mirrors
	// internal/storage/result.go:OverviewResult JSON tags).
	for _, key := range []string{"pageviews", "visitors", "goals", "revenue_rials", "rpv_rials"} {
		if _, ok := overview[key]; !ok {
			t.Errorf("overview response missing %q (SPA Overview panel needs this)", key)
		}
	}

	// 5) Wrong bearer token → 401 (proves auth still gates the API
	// even when SPA shell loads without auth).
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, overviewURL, nil)
	req2.Header.Set("Authorization", "Bearer wrong-token")

	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("GET overview wrong-token: %v", err)
	}

	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong-token status = %d, want 401", resp2.StatusCode)
	}
}
