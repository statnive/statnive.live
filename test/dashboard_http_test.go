//go:build integration

// Dashboard HTTP integration test — proves the full request path:
// chi router → bearer-token middleware → rate limiter → handler →
// CachedStore → ClickHouse → JSON response. Asserts cross-tenant
// isolation via URL manipulation, 501 for not-implemented endpoints,
// 400 for bad input, 401 for missing bearer token, and that the
// SEO query's WITH FILL produces a row per day in the requested range.
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
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/dashboard"
	"github.com/statnive/statnive.live/internal/ratelimit"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
	"github.com/statnive/statnive.live/internal/storage/storagetest"
)

const (
	dashboardSiteA = uint32(501)
	dashboardSiteB = uint32(502)
	dashboardHost  = "dashboard-test.example.com"
)

func TestDashboardHTTP_OverviewShape(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, _ := newDashboardTestServer(t, ctx, "")

	now := time.Now().UTC().Truncate(time.Hour)
	url := fmt.Sprintf("%s/api/stats/overview?site=%d&from=%s&to=%s",
		srv.URL, dashboardSiteA,
		now.Add(-7*24*time.Hour).Format("2006-01-02"),
		now.Add(24*time.Hour).Format("2006-01-02"))

	resp := getJSON(t, url, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, key := range []string{"pageviews", "visitors", "goals", "revenue_rials", "rpv_rials"} {
		if _, ok := got[key]; !ok {
			t.Errorf("response missing %q: %v", key, got)
		}
	}
}

func TestDashboardHTTP_NotImplemented(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, _ := newDashboardTestServer(t, ctx, "")

	for _, path := range []string{"/api/stats/geo", "/api/stats/devices", "/api/stats/funnel"} {
		url := fmt.Sprintf("%s%s?site=%d", srv.URL, path, dashboardSiteA)

		resp := getJSON(t, url, "")
		if resp.StatusCode != http.StatusNotImplemented {
			t.Errorf("%s status = %d, want 501", path, resp.StatusCode)
		}
	}
}

func TestDashboardHTTP_BadInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, _ := newDashboardTestServer(t, ctx, "")

	cases := map[string]string{
		"missing site":   "/api/stats/overview",
		"unparseable":    "/api/stats/overview?site=1&from=not-a-date",
		"site = 0":       "/api/stats/overview?site=0",
		"range too big":  "/api/stats/overview?site=1&from=2024-01-01&to=2026-04-19",
	}

	for label, path := range cases {
		t.Run(label, func(t *testing.T) {
			resp := getJSON(t, srv.URL+path, "")
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("%s status = %d, want 400", path, resp.StatusCode)
			}
		})
	}
}

func TestDashboardHTTP_BearerTokenEnforced(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const token = "test-shared-secret"
	srv, _ := newDashboardTestServer(t, ctx, token)

	url := fmt.Sprintf("%s/api/stats/overview?site=%d", srv.URL, dashboardSiteA)

	if got := getJSON(t, url, "").StatusCode; got != http.StatusUnauthorized {
		t.Errorf("missing token: status = %d, want 401", got)
	}

	if got := getJSON(t, url, "wrong-token").StatusCode; got != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", got)
	}

	if got := getJSON(t, url, token).StatusCode; got != http.StatusOK {
		t.Errorf("correct token: status = %d, want 200", got)
	}
}

func TestDashboardHTTP_CrossTenantIsolation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, store := newDashboardTestServer(t, ctx, "")

	now := time.Now().UTC().Truncate(time.Hour)

	// Site A gets one event on /a-only; site B gets one on /b-only.
	storagetest.WriteEvents(t, ctx, store.Conn(), []storagetest.SeedEvent{
		{
			SiteID: dashboardSiteA, Time: now, Pathname: "/a-only",
			Channel: "Direct", VisitorHash: [16]byte{1},
		},
		{
			SiteID: dashboardSiteB, Time: now, Pathname: "/b-only",
			Channel: "Direct", VisitorHash: [16]byte{2},
		},
	})

	url := fmt.Sprintf("%s/api/stats/pages?site=%d", srv.URL, dashboardSiteA)
	resp := getJSON(t, url, "")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var pages []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&pages); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, p := range pages {
		if p["pathname"] == "/b-only" {
			t.Errorf("CRITICAL: siteA Pages leaked siteB pathname via URL: %v", p)
		}
	}
}

func TestDashboardHTTP_SEOWithFill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, store := newDashboardTestServer(t, ctx, "")

	// Seed organic events on day 0, day 3, day 6 of a 7-day window.
	now := time.Now().UTC().Truncate(24 * time.Hour)
	from := now.Add(-6 * 24 * time.Hour)

	events := []storagetest.SeedEvent{
		{SiteID: dashboardSiteA, Time: from, Pathname: "/seo-0", Referrer: "https://google.com/", ReferrerName: "google", Channel: "Organic Search", VisitorHash: [16]byte{0xa}},
		{SiteID: dashboardSiteA, Time: from.Add(3 * 24 * time.Hour), Pathname: "/seo-3", Referrer: "https://google.com/", ReferrerName: "google", Channel: "Organic Search", VisitorHash: [16]byte{0xb}},
		{SiteID: dashboardSiteA, Time: from.Add(6 * 24 * time.Hour), Pathname: "/seo-6", Referrer: "https://google.com/", ReferrerName: "google", Channel: "Organic Search", VisitorHash: [16]byte{0xc}},
	}

	storagetest.WriteEvents(t, ctx, store.Conn(), events)

	url := fmt.Sprintf("%s/api/stats/seo?site=%d&from=%s&to=%s",
		srv.URL, dashboardSiteA,
		from.Format("2006-01-02"),
		now.Add(24*time.Hour).Format("2006-01-02"))

	resp := getJSON(t, url, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// WITH FILL emits one row per day in the [from, to) range = 7 days.
	if len(rows) < 7 {
		t.Errorf("WITH FILL produced %d rows, want >= 7 (one per day)", len(rows))
	}
}

func TestDashboardHTTP_SitesList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, _ := newDashboardTestServer(t, ctx, "")

	resp := getJSON(t, srv.URL+"/api/sites", "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var env struct {
		Sites []map[string]any `json:"sites"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Seed in newDashboardTestServer adds siteA + siteB; list MUST contain
	// both with the migration-003 default tz = "Asia/Tehran".
	found := map[uint32]map[string]any{}

	for _, s := range env.Sites {
		id, _ := s["id"].(float64)
		found[uint32(id)] = s
	}

	for _, want := range []uint32{dashboardSiteA, dashboardSiteB} {
		got, ok := found[want]
		if !ok {
			t.Errorf("site %d missing from /api/sites response: %v", want, env.Sites)

			continue
		}

		if tz, _ := got["tz"].(string); tz != "Asia/Tehran" {
			t.Errorf("site %d: tz = %q, want Asia/Tehran (migration-003 default)", want, tz)
		}

		if enabled, _ := got["enabled"].(bool); !enabled {
			t.Errorf("site %d: enabled = false, want true (seed default)", want)
		}
	}
}

func TestDashboardHTTP_TrendShape(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, _ := newDashboardTestServer(t, ctx, "")

	now := time.Now().UTC().Truncate(time.Hour)
	url := fmt.Sprintf("%s/api/stats/trend?site=%d&from=%s&to=%s",
		srv.URL, dashboardSiteA,
		now.Add(-7*24*time.Hour).Format("2006-01-02"),
		now.Add(24*time.Hour).Format("2006-01-02"))

	resp := getJSON(t, url, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(rows) == 0 {
		t.Fatalf("trend returned zero rows — WITH FILL should emit a row per day")
	}

	for i, r := range rows {
		for _, key := range []string{"day", "visitors", "pageviews"} {
			if _, ok := r[key]; !ok {
				t.Errorf("row[%d] missing %q: %v", i, key, r)
			}
		}
	}
}

func TestDashboardHTTP_TrendGapFilling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, store := newDashboardTestServer(t, ctx, "")

	// Seed events on day 0 and day 6 of a 7-day window — days 1-5 are
	// empty. WITH FILL must emit rows for the empty days (pageviews=0,
	// visitors=0) so the SPA's uPlot chart has a continuous x-axis.
	now := time.Now().UTC().Truncate(24 * time.Hour)
	from := now.Add(-6 * 24 * time.Hour)

	events := []storagetest.SeedEvent{
		{SiteID: dashboardSiteA, Time: from, Pathname: "/trend-0", Channel: "Direct", VisitorHash: [16]byte{0xa}},
		{SiteID: dashboardSiteA, Time: from.Add(6 * 24 * time.Hour), Pathname: "/trend-6", Channel: "Direct", VisitorHash: [16]byte{0xb}},
	}
	storagetest.WriteEvents(t, ctx, store.Conn(), events)

	url := fmt.Sprintf("%s/api/stats/trend?site=%d&from=%s&to=%s",
		srv.URL, dashboardSiteA,
		from.Format("2006-01-02"),
		now.Add(24*time.Hour).Format("2006-01-02"))

	resp := getJSON(t, url, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// [from, to) = 7 days. WITH FILL emits one row per day — may be 7 or
	// 8 depending on IRST/UTC midnight alignment; anything less than 7
	// proves the fill regressed.
	if len(rows) < 7 {
		t.Errorf("WITH FILL produced %d rows, want >= 7 (gap-filled days inclusive)", len(rows))
	}

	var zeros int

	for _, r := range rows {
		views, _ := r["pageviews"].(float64)
		visitors, _ := r["visitors"].(float64)

		if views == 0 && visitors == 0 {
			zeros++
		}
	}

	if zeros == 0 {
		t.Errorf("expected at least one gap-filled zero row (days 1-5 had no events); got all non-zero: %v", rows)
	}
}

func TestDashboardHTTP_SourcesChannelFilter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, store := newDashboardTestServer(t, ctx, "")

	now := time.Now().UTC().Truncate(time.Hour)

	// Seed events on two channels for the same site. Channel filter
	// must narrow the Sources result set to the requested channel only.
	storagetest.WriteEvents(t, ctx, store.Conn(), []storagetest.SeedEvent{
		{SiteID: dashboardSiteA, Time: now, Pathname: "/a", Referrer: "(direct)", ReferrerName: "(direct)", Channel: "Direct", VisitorHash: [16]byte{1}},
		{SiteID: dashboardSiteA, Time: now, Pathname: "/b", Referrer: "https://google.com/", ReferrerName: "google", Channel: "Organic Search", VisitorHash: [16]byte{2}},
	})

	url := fmt.Sprintf("%s/api/stats/sources?site=%d&channel=Direct", srv.URL, dashboardSiteA)
	resp := getJSON(t, url, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(rows) == 0 {
		t.Fatalf("channel=Direct returned zero rows; expected at least the direct row")
	}

	for _, r := range rows {
		if ch, _ := r["channel"].(string); ch != "Direct" {
			t.Errorf("channel filter leaked row with channel=%q: %v", ch, r)
		}
	}
}

func TestDashboardHTTP_PagesPathFilter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, store := newDashboardTestServer(t, ctx, "")

	now := time.Now().UTC().Truncate(time.Hour)

	storagetest.WriteEvents(t, ctx, store.Conn(), []storagetest.SeedEvent{
		{SiteID: dashboardSiteA, Time: now, Pathname: "/blog/intro", Channel: "Direct", VisitorHash: [16]byte{3}},
		{SiteID: dashboardSiteA, Time: now, Pathname: "/pricing", Channel: "Direct", VisitorHash: [16]byte{4}},
	})

	url := fmt.Sprintf("%s/api/stats/pages?site=%d&path=/blog/intro", srv.URL, dashboardSiteA)
	resp := getJSON(t, url, "")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(rows) == 0 {
		t.Fatalf("path=/blog/intro returned zero rows; expected the match")
	}

	for _, r := range rows {
		if p, _ := r["pathname"].(string); p != "/blog/intro" {
			t.Errorf("path filter leaked row with pathname=%q: %v", p, r)
		}
	}
}

// --- shared test helpers ---

// newDashboardTestServer wires the chi router that production runs in
// main.go: rate limit + optional bearer token + dashboard.Mount with a
// CachedStore over the live ClickHouse. Auth token is the bearer
// shared secret; pass "" to disable auth (dev mode).
func newDashboardTestServer(t *testing.T, ctx context.Context, bearerToken string) (*httptest.Server, *storage.ClickHouseStore) {
	t.Helper()

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

	storagetest.CleanSiteEvents(t, ctx, store.Conn(), dashboardSiteA)
	storagetest.CleanSiteEvents(t, ctx, store.Conn(), dashboardSiteB)
	storagetest.SeedSite(t, ctx, store.Conn(), dashboardSiteA, dashboardHost)
	storagetest.SeedSite(t, ctx, store.Conn(), dashboardSiteB, dashboardHost+".b")

	auditLog, err := audit.New(t.TempDir() + "/audit.jsonl")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	t.Cleanup(func() { _ = auditLog.Close() })

	cached := storage.NewCachedStore(storage.NewClickhouseQueryStore(store), 256)

	rateLimitMW, err := ratelimit.Middleware(6000, time.Minute, ratelimit.Config{Audit: auditLog})
	if err != nil {
		t.Fatalf("ratelimit: %v", err)
	}

	// Phase 2b replaced the single-token bearer middleware with
	// auth.APITokenMiddleware + RequireAuthenticated. The empty-token
	// test mode (tests that don't care about auth) previously got the
	// no-op middleware; preserve that shape so the existing table of
	// tests that pass bearerToken="" stays on the happy path.
	authMW := noopMiddleware

	if bearerToken != "" {
		tokenHash := sha256.Sum256([]byte(bearerToken))
		authDeps := auth.MiddlewareDeps{
			Audit: auditLog,
			APITokens: []auth.APIToken{
				{
					TokenHashHex: hex.EncodeToString(tokenHash[:]),
					SiteID:       0,
					Label:        "integration-test",
				},
			},
		}
		apiTokenMW := auth.APITokenMiddleware(authDeps)
		requireAuthed := auth.RequireAuthenticated(auditLog)
		authMW = func(next http.Handler) http.Handler {
			return apiTokenMW(requireAuthed(next))
		}
	}

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(rateLimitMW)
		r.Use(authMW)
		dashboard.Mount(r, dashboard.Deps{
			Store:  cached,
			Sites:  sites.New(store.Conn()),
			Audit:  auditLog,
			Logger: logger,
		})
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return srv, store
}

func noopMiddleware(next http.Handler) http.Handler { return next }

func getJSON(t *testing.T, url, bearerToken string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	t.Cleanup(func() { _ = resp.Body.Close() })

	return resp
}
