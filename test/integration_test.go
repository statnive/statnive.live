//go:build integration

// Integration smoke: 100 events → HTTP handler → WAL → consumer → ClickHouse.
// Requires `docker compose -f deploy/docker-compose.dev.yml up -d clickhouse`.
// Run with: make test-integration
//
// Scope: Phase 0/1 slice acceptance — proves the full pipeline shape runs
// end-to-end. Cross-site isolation, rollup correctness, and enrichment-order
// assertions land in subsequent slices per PLAN.md.
package integration_test

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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/enrich"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/privacy"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
)

const (
	testHostname    = "integration-test.example.com"
	testSiteID      = 42
	eventCount      = 100
	// Bumped from 5s to 15s in Phase 5a. After TestIngestPipelineSmoke
	// drains 100 events + the consumer's Close waits for fsync, CI runs
	// occasionally hit the 5s ceiling for the NEXT test's first event
	// (multitenant flake seen on PR #28 + PR #29). 15s is still tight
	// enough to catch a real deadlock; happy path hits in <1s.
	flushTimeout    = 15 * time.Second
	defaultCHAddr   = "127.0.0.1:19000" // matches deploy/docker-compose.dev.yml
	testDatabase    = "statnive"
	testHTTPTimeout = 2 * time.Second
)

func clickhouseAddr() string {
	if v := os.Getenv("STATNIVE_CLICKHOUSE_ADDR"); v != "" {
		return v
	}

	return defaultCHAddr
}

func TestIngestPipelineSmoke(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs:    []string{clickhouseAddr()},
		Database: testDatabase,
		Username: "default",
	}, logger)
	if err != nil {
		t.Fatalf("clickhouse open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	migrator := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{
		Database: testDatabase,
	}, logger)

	if migErr := migrator.Run(ctx); migErr != nil {
		t.Fatalf("migrate: %v", migErr)
	}

	// Clean slate for the integration site to keep re-runs deterministic.
	// mutations_sync=2 blocks until the DELETE merge completes.
	if cleanErr := store.Conn().Exec(ctx,
		`ALTER TABLE statnive.events_raw DELETE WHERE site_id = ? SETTINGS mutations_sync = 2`,
		uint32(testSiteID),
	); cleanErr != nil {
		t.Logf("delete-existing warning (ok on first run): %v", cleanErr)
	}

	if upsertErr := store.Conn().Exec(ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (?, ?, ?, 1)`,
		uint32(testSiteID), testHostname, "integration-test",
	); upsertErr != nil {
		t.Fatalf("seed site: %v", upsertErr)
	}

	walDir := t.TempDir()

	wal, err := ingest.NewWALWriter(ingest.WALConfig{Dir: filepath.Join(walDir, "wal")}, logger)
	if err != nil {
		t.Fatalf("wal: %v", err)
	}
	t.Cleanup(func() { _ = wal.Close() })

	saltMgr, err := identity.NewSaltManager([]byte("integration-test-master-secret-32"))
	if err != nil {
		t.Fatalf("salt manager: %v", err)
	}

	geoIP, err := enrich.NewGeoIPEnricher("", logger) // no-op (no DB)
	if err != nil {
		t.Fatalf("geoip: %v", err)
	}
	t.Cleanup(func() { _ = geoIP.Close() })

	channelMapper, err := enrich.NewChannelMapper("../config/sources.yaml")
	if err != nil {
		t.Fatalf("channel mapper: %v", err)
	}
	t.Cleanup(channelMapper.Close)

	pipeline := enrich.NewPipeline(enrich.Deps{
		Salt:    saltMgr,
		Bloom:   enrich.NewNewVisitorFilter(10000, 0.001),
		GeoIP:   geoIP,
		UA:      enrich.NewUAParser(),
		Bot:     enrich.NewBotDetector(logger),
		Channel: channelMapper,
		Logger:  logger,
	})

	groupSyncer := ingest.NewGroupSyncer(wal, ingest.GroupConfig{}, nil, logger)
	t.Cleanup(groupSyncer.Close)

	consumer := ingest.NewConsumer(groupSyncer.Out(), wal, store, ingest.ConsumerConfig{
		BatchRows:     50,
		BatchInterval: 100 * time.Millisecond,
		DrainSettle:   100 * time.Millisecond,
	}, nil, logger)

	consumerDone := make(chan struct{})
	go func() {
		consumer.Run(ctx)
		close(consumerDone)
	}()

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(ingest.FastRejectMiddleware(nil, nil))
		r.Method(http.MethodPost, "/api/event", ingest.NewHandler(ingest.HandlerConfig{
			Pipeline: pipeline,
			WAL:      groupSyncer,
			Sites:    sites.New(store.Conn()),
			Logger:   logger,
		}))
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	client := &http.Client{Timeout: testHTTPTimeout}

	for i := 0; i < eventCount; i++ {
		body, _ := json.Marshal(ingest.RawEvent{
			Hostname:  testHostname,
			Pathname:  fmt.Sprintf("/page-%03d", i),
			EventType: "pageview",
			EventName: "pageview",
		})

		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/event", bytes.NewReader(body))
		if reqErr != nil {
			t.Fatalf("request %d: %v", i, reqErr)
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (IntegrationTest/1.0) BrowserLike")
		req.Header.Set("Content-Type", "text/plain")

		resp, doErr := client.Do(req)
		if doErr != nil {
			t.Fatalf("POST %d: %v", i, doErr)
		}

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST %d status = %d, want 202", i, resp.StatusCode)
		}

		_ = resp.Body.Close()
	}

	// Give the batcher time to flush (100ms interval + some CH latency).
	waitForCount(t, ctx, store, testSiteID, eventCount, flushTimeout)

	// Shutdown: cancel ctx → groupSyncer cleanup closes Out →
	// consumer.Run sees the close + exits.
	cancel()
	<-consumerDone
}

func waitForCount(t *testing.T, parent context.Context, store *storage.ClickHouseStore, siteID uint32, want int, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		var got uint64

		row := store.Conn().QueryRow(ctx,
			`SELECT count() FROM statnive.events_raw WHERE site_id = ?`, siteID,
		)

		if scanErr := row.Scan(&got); scanErr == nil && got >= uint64(want) {
			if got != uint64(want) {
				t.Fatalf("got %d rows, want exactly %d", got, want)
			}

			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %d rows in events_raw (site_id=%d)", want, siteID)
		case <-ticker.C:
		}
	}
}

// integrationStack wires CH + WAL + consumer + ingest handler + privacy
// handlers onto a single chi router and returns the httptest server.
// The Stage-4 privacy-fix tests share this scaffolding because each
// flow needs all three of (a) ingest /api/event, (b) privacy /opt-out
// and /consent, and (c) the suppression list shared between them.
type integrationStack struct {
	t           *testing.T
	ctx         context.Context
	cancel      context.CancelFunc
	store       *storage.ClickHouseStore
	srv         *httptest.Server
	consumerEnd <-chan struct{}
	masterKey   []byte
	suppression *privacy.SuppressionList
}

const stage4MasterSecret = "stage4-integration-master-secret-32"

func newIntegrationStack(t *testing.T, siteID uint32, hostname string) *integrationStack {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs:    []string{clickhouseAddr()},
		Database: testDatabase,
		Username: "default",
	}, logger)
	if err != nil {
		cancel()
		t.Fatalf("clickhouse open: %v", err)
	}

	t.Cleanup(func() { _ = store.Close() })

	migrator := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{
		Database: testDatabase,
	}, logger)

	if migErr := migrator.Run(ctx); migErr != nil {
		cancel()
		t.Fatalf("migrate: %v", migErr)
	}

	// Per-test clean slate keyed by site_id keeps the three Stage-4
	// tests from contaminating one another even when they share the
	// same ClickHouse instance.
	if cleanErr := store.Conn().Exec(ctx,
		`ALTER TABLE statnive.events_raw DELETE WHERE site_id = ? SETTINGS mutations_sync = 2`,
		siteID,
	); cleanErr != nil {
		t.Logf("delete-existing warning (ok on first run): %v", cleanErr)
	}

	if upsertErr := store.Conn().Exec(ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (?, ?, ?, 1)`,
		siteID, hostname, hostname,
	); upsertErr != nil {
		cancel()
		t.Fatalf("seed site: %v", upsertErr)
	}

	walDir := t.TempDir()

	wal, err := ingest.NewWALWriter(ingest.WALConfig{Dir: filepath.Join(walDir, "wal")}, logger)
	if err != nil {
		cancel()
		t.Fatalf("wal: %v", err)
	}

	t.Cleanup(func() { _ = wal.Close() })

	masterKey := []byte(stage4MasterSecret)

	saltMgr, err := identity.NewSaltManager(masterKey)
	if err != nil {
		cancel()
		t.Fatalf("salt manager: %v", err)
	}

	geoIP, err := enrich.NewGeoIPEnricher("", logger)
	if err != nil {
		cancel()
		t.Fatalf("geoip: %v", err)
	}

	t.Cleanup(func() { _ = geoIP.Close() })

	channelMapper, err := enrich.NewChannelMapper("../config/sources.yaml")
	if err != nil {
		cancel()
		t.Fatalf("channel mapper: %v", err)
	}

	t.Cleanup(channelMapper.Close)

	pipeline := enrich.NewPipeline(enrich.Deps{
		Salt:    saltMgr,
		Bloom:   enrich.NewNewVisitorFilter(10000, 0.001),
		GeoIP:   geoIP,
		UA:      enrich.NewUAParser(),
		Bot:     enrich.NewBotDetector(logger),
		Channel: channelMapper,
		Logger:  logger,
	})

	groupSyncer := ingest.NewGroupSyncer(wal, ingest.GroupConfig{}, nil, logger)
	t.Cleanup(groupSyncer.Close)

	consumer := ingest.NewConsumer(groupSyncer.Out(), wal, store, ingest.ConsumerConfig{
		BatchRows:     50,
		BatchInterval: 100 * time.Millisecond,
		DrainSettle:   100 * time.Millisecond,
	}, nil, logger)

	consumerDone := make(chan struct{})

	go func() {
		consumer.Run(ctx)
		close(consumerDone)
	}()

	suppression, err := privacy.NewSuppressionList(filepath.Join(walDir, "suppression.wal"))
	if err != nil {
		cancel()
		t.Fatalf("suppression: %v", err)
	}

	t.Cleanup(func() { _ = suppression.Close() })

	registry := sites.New(store.Conn())

	privacyHandlers, err := privacy.NewHandlers(privacy.Config{
		Sites:        registry,
		MasterSecret: masterKey,
		Suppression:  suppression,
	})
	if err != nil {
		cancel()
		t.Fatalf("privacy handlers: %v", err)
	}

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(ingest.FastRejectMiddleware(nil, nil))
		r.Method(http.MethodPost, "/api/event", ingest.NewHandler(ingest.HandlerConfig{
			Pipeline:     pipeline,
			WAL:          groupSyncer,
			Sites:        registry,
			MasterSecret: masterKey,
			Suppression:  suppression,
			Logger:       logger,
		}))
	})
	router.Method(http.MethodPost, "/api/privacy/opt-out", http.HandlerFunc(privacyHandlers.OptOut))
	router.Method(http.MethodPost, "/api/privacy/consent", http.HandlerFunc(privacyHandlers.Consent))

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &integrationStack{
		t:           t,
		ctx:         ctx,
		cancel:      cancel,
		store:       store,
		srv:         srv,
		consumerEnd: consumerDone,
		masterKey:   masterKey,
		suppression: suppression,
	}
}

// shutdown stops the consumer cleanly. Idempotent; safe to call from a
// defer alongside the t.Cleanup hooks.
func (s *integrationStack) shutdown() {
	s.cancel()
	<-s.consumerEnd
}

// postJSON fires a JSON POST and returns the parsed response cookies +
// status. Tiny helper to keep the new tests readable.
func postJSON(t *testing.T, srv *httptest.Server, path, body string, cookies []*http.Cookie, hostHeader string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (IntegrationTest/Stage4)")

	if hostHeader != "" {
		req.Host = hostHeader
	}

	for _, c := range cookies {
		req.AddCookie(c)
	}

	resp, err := (&http.Client{Timeout: testHTTPTimeout}).Do(req)
	if err != nil {
		t.Fatalf("do %s: %v", path, err)
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	return resp
}

func TestConsent_GiveThenIngest_PostConsentIdentifierLands(t *testing.T) {
	const (
		siteID   uint32 = 4242
		hostname        = "stage4-consent.example.com"
	)

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	// 1. Fresh visitor — no _statnive, no consent cookies.
	resp := postJSON(t, stack.srv, "/api/privacy/consent", `{"action":"give"}`, nil, hostname)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("/api/privacy/consent status = %d, want 204", resp.StatusCode)
	}

	var (
		statniveCookie *http.Cookie
		consentCookie  *http.Cookie
	)

	for _, c := range resp.Cookies() {
		switch c.Name {
		case "_statnive":
			statniveCookie = c
		case fmt.Sprintf("_statnive_consent_%d", siteID):
			consentCookie = c
		}
	}

	if statniveCookie == nil || statniveCookie.Value == "" {
		t.Fatalf("_statnive cookie not minted by consent give")
	}

	if consentCookie == nil || consentCookie.Value != "v1" {
		t.Fatalf("per-site consent cookie missing or wrong value")
	}

	// 2. Fire 10 events carrying the freshly-minted cookies. X-Statnive-
	// Consent: given flips allowIdentity on in the ingest handler so the
	// _statnive is re-read (not overwritten) and the CookieID hash lands
	// in events_raw.
	for i := range 10 {
		body, _ := json.Marshal(ingest.RawEvent{
			Hostname:  hostname,
			Pathname:  fmt.Sprintf("/page-%02d", i),
			EventType: "pageview",
			EventName: "pageview",
		})

		req, err := http.NewRequest(http.MethodPost, stack.srv.URL+"/api/event", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("event %d req: %v", i, err)
		}

		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Stage4Test) BrowserLike")
		req.Header.Set("X-Statnive-Consent", "given")
		req.AddCookie(statniveCookie)
		req.AddCookie(consentCookie)

		ev, err := (&http.Client{Timeout: testHTTPTimeout}).Do(req)
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}

		_, _ = io.Copy(io.Discard, ev.Body)
		_ = ev.Body.Close()

		if ev.StatusCode != http.StatusAccepted {
			t.Fatalf("event %d status = %d, want 202", i, ev.StatusCode)
		}
	}

	// 3. ClickHouse-Oracle. 10 rows land, all hash-prefixed, one
	// distinct identifier (server-minted UUID reused on every request).
	waitForCount(t, stack.ctx, stack.store, siteID, 10, flushTimeout)

	var (
		hashed uint64
		uniq   uint64
	)

	row := stack.store.Conn().QueryRow(stack.ctx,
		`SELECT countIf(cookie_id LIKE 'h:%'), uniq(cookie_id)
		 FROM statnive.events_raw WHERE site_id = ?`, siteID)
	if err := row.Scan(&hashed, &uniq); err != nil {
		t.Fatalf("oracle scan: %v", err)
	}

	if hashed != 10 {
		t.Errorf("cookie_id LIKE 'h:%%' rows = %d, want 10", hashed)
	}

	if uniq != 1 {
		t.Errorf("distinct cookie_id = %d, want 1 (server-minted UUID reused)", uniq)
	}
}

func TestOptOut_NoCookieThenIngest_EventsDropped(t *testing.T) {
	const (
		siteID   uint32 = 4243
		hostname        = "stage4-optout.example.com"
	)

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	// 1. Fresh visitor opt-out — no _statnive cookie.
	resp := postJSON(t, stack.srv, "/api/privacy/opt-out", ``, nil, hostname)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("/api/privacy/opt-out status = %d, want 204", resp.StatusCode)
	}

	var optoutCookie *http.Cookie

	wantName := fmt.Sprintf("_statnive_optout_%d", siteID)

	for _, c := range resp.Cookies() {
		if c.Name == wantName {
			optoutCookie = c
			break
		}
	}

	if optoutCookie == nil || optoutCookie.Value != "v1" {
		t.Fatalf("missing per-site opt-out cookie")
	}

	// No _statnive present → suppression list still empty (cookie alone
	// IS the signal at the ingest gate).
	if got := stack.suppression.Len(); got != 0 {
		t.Errorf("suppression Len = %d, want 0", got)
	}

	// 2. Fire 5 events carrying ONLY the opt-out cookie (no _statnive).
	for i := range 5 {
		body, _ := json.Marshal(ingest.RawEvent{
			Hostname:  hostname,
			Pathname:  fmt.Sprintf("/page-%02d", i),
			EventType: "pageview",
			EventName: "pageview",
		})

		req, err := http.NewRequest(http.MethodPost, stack.srv.URL+"/api/event", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("event %d req: %v", i, err)
		}

		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Stage4Test) BrowserLike")
		req.AddCookie(optoutCookie)

		ev, err := (&http.Client{Timeout: testHTTPTimeout}).Do(req)
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}

		_, _ = io.Copy(io.Discard, ev.Body)
		_ = ev.Body.Close()

		if ev.StatusCode != http.StatusAccepted {
			t.Fatalf("event %d status = %d, want 202 (response-shape stable on drop)", i, ev.StatusCode)
		}
	}

	// 3. ClickHouse-Oracle: events were dropped at the ingest gate
	// (cookie-based short-circuit). Give the batcher a moment to settle
	// so any false-positive write would have time to land.
	time.Sleep(500 * time.Millisecond)

	var rows uint64

	row := stack.store.Conn().QueryRow(stack.ctx,
		`SELECT count() FROM statnive.events_raw WHERE site_id = ?`, siteID)
	if err := row.Scan(&rows); err != nil {
		t.Fatalf("oracle scan: %v", err)
	}

	if rows != 0 {
		t.Errorf("events_raw rows for opted-out site = %d, want 0", rows)
	}
}

func TestConsent_GiveTenancyIsolation(t *testing.T) {
	const (
		siteAID   uint32 = 4244
		siteBID   uint32 = 4245
		hostnameA        = "stage4-tenancy-a.example.com"
		hostnameB        = "stage4-tenancy-b.example.com"
	)

	stackA := newIntegrationStack(t, siteAID, hostnameA)
	defer stackA.shutdown()

	// Seed site B inside the same backing CH so both sites_resolvers
	// see both rows. Reuses stackA's store + registry; we only need a
	// second logical hostname to fire from.
	if upsertErr := stackA.store.Conn().Exec(stackA.ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (?, ?, ?, 1)`,
		siteBID, hostnameB, hostnameB,
	); upsertErr != nil {
		t.Fatalf("seed site B: %v", upsertErr)
	}

	// Same rawID injected at the cookie layer to both sites. The hash
	// MUST diverge because identity.HexCookieIDHash mixes site_id into
	// the key — multi-tenancy isolation invariant.
	const sharedRawID = "550e8400-e29b-41d4-a716-446655440000"

	cookie := &http.Cookie{Name: "_statnive", Value: sharedRawID}

	respA := postJSON(t, stackA.srv, "/api/privacy/consent", `{"action":"give"}`, []*http.Cookie{cookie}, hostnameA)
	if respA.StatusCode != http.StatusNoContent {
		t.Fatalf("site A consent give = %d", respA.StatusCode)
	}

	respB := postJSON(t, stackA.srv, "/api/privacy/consent", `{"action":"give"}`, []*http.Cookie{cookie}, hostnameB)
	if respB.StatusCode != http.StatusNoContent {
		t.Fatalf("site B consent give = %d", respB.StatusCode)
	}

	// Per-site consent cookies must carry the site-specific name.
	gotA := cookieByName(respA.Cookies(), fmt.Sprintf("_statnive_consent_%d", siteAID))
	gotB := cookieByName(respB.Cookies(), fmt.Sprintf("_statnive_consent_%d", siteBID))

	if gotA == nil || gotA.Value != "v1" {
		t.Errorf("site A consent cookie missing")
	}

	if gotB == nil || gotB.Value != "v1" {
		t.Errorf("site B consent cookie missing")
	}

	// Cookies must NOT carry the other site's per-site name.
	if leak := cookieByName(respA.Cookies(), fmt.Sprintf("_statnive_consent_%d", siteBID)); leak != nil {
		t.Errorf("site A response leaked site B consent cookie")
	}

	if leak := cookieByName(respB.Cookies(), fmt.Sprintf("_statnive_consent_%d", siteAID)); leak != nil {
		t.Errorf("site B response leaked site A consent cookie")
	}

	// Hash-divergence assertion. Compute what the server would have
	// hashed for each site and confirm they differ — proves the master
	// secret + site_id keying actually separates tenants at rest.
	hashA := identity.HexCookieIDHash(stackA.masterKey, siteAID, sharedRawID)
	hashB := identity.HexCookieIDHash(stackA.masterKey, siteBID, sharedRawID)

	if hashA == "" || hashB == "" {
		t.Fatalf("hash helper returned empty")
	}

	if hashA == hashB {
		t.Errorf("hashA == hashB for identical UUID across sites — tenancy isolation BROKEN")
	}
}

func cookieByName(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}

	return nil
}

// TestMain is a placeholder — we don't need setup/teardown at the package
// level yet, but leaving the hook makes it obvious where future test-scope
// migrations go.
func TestMain(m *testing.M) {
	// Skip all integration tests if docker-compose ClickHouse isn't reachable.
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{clickhouseAddr()},
		Auth: clickhouse.Auth{Database: testDatabase, Username: "default"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration: clickhouse open failed, skipping:", err)
		os.Exit(0)
	}

	pctx, pcancel := context.WithTimeout(context.Background(), testHTTPTimeout)
	pingErr := conn.Ping(pctx)

	pcancel()
	_ = conn.Close()

	if pingErr != nil {
		fmt.Fprintln(os.Stderr, "integration: clickhouse ping failed, skipping:", pingErr)
		os.Exit(0)
	}

	os.Exit(m.Run())
}
