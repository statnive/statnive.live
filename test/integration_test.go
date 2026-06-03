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

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/audit/audittest"
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
	t             *testing.T
	ctx           context.Context
	cancel        context.CancelFunc
	store         *storage.ClickHouseStore
	srv           *httptest.Server
	consumerEnd   <-chan struct{}
	masterKey     []byte
	suppression   *privacy.SuppressionList
	privacyErase  *privacy.EraseEnumerator
	privacyExport *privacy.VisitorExporter
	auditPath     string
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

	auditPath := filepath.Join(walDir, "audit.jsonl")

	auditLog, err := audit.New(auditPath)
	if err != nil {
		cancel()
		t.Fatalf("audit log: %v", err)
	}

	t.Cleanup(func() { _ = auditLog.Close() })

	privacyErase := privacy.NewEraseEnumerator(store.Conn(), testDatabase)
	privacyExport := privacy.NewVisitorExporter(store.Conn(), testDatabase)

	privacyHandlers, err := privacy.NewHandlers(privacy.Config{
		Sites:        registry,
		MasterSecret: masterKey,
		Suppression:  suppression,
		Erase:        privacyErase,
		Export:       privacyExport,
		Audit:        auditLog,
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
	router.Method(http.MethodPost, "/api/privacy/erase", http.HandlerFunc(privacyHandlers.Erase))
	router.Method(http.MethodGet, "/api/privacy/access", http.HandlerFunc(privacyHandlers.Access))

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &integrationStack{
		t:             t,
		ctx:           ctx,
		cancel:        cancel,
		store:         store,
		srv:           srv,
		consumerEnd:   consumerDone,
		masterKey:     masterKey,
		suppression:   suppression,
		privacyErase:  privacyErase,
		privacyExport: privacyExport,
		auditPath:     auditPath,
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

// TestDSAR_CrossTenantIsolation pins the security invariant fixed by
// the v0.0.36 hot-fix: an Art. 17 erase request on site A MUST NOT
// delete site B's rows even when both sites happen to carry the same
// cookie_id hash (statistically impossible organically, but
// constructible by an insider with master_secret access — see
// audit/legal-vs-system-audit.md FAIL-1).
func TestDSAR_CrossTenantIsolation(t *testing.T) {
	const (
		siteAID   uint32 = 4444
		siteBID   uint32 = 4445
		hostnameA        = "stage4-erase-a.example.com"
		hostnameB        = "stage4-erase-b.example.com"
	)

	stack := newIntegrationStack(t, siteAID, hostnameA)
	defer stack.shutdown()

	// Seed site B in the same backing ClickHouse so both sites resolve.
	if err := stack.store.Conn().Exec(stack.ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (?, ?, ?, 1)`,
		siteBID, hostnameB, hostnameB,
	); err != nil {
		t.Fatalf("seed site B: %v", err)
	}

	// Construct the cross-tenant collision: same cookie_id hash on
	// rows for BOTH sites. Organic collisions are infeasible because
	// HexCookieIDHash mixes site_id into SHA-256 input, so we insert
	// raw events_raw rows with a hand-crafted hash to simulate the
	// insider-attack scenario. Use a per-run unique hash so this test
	// doesn't collide with other tests sharing the same ClickHouse.
	sharedHash := fmt.Sprintf("h:test-cross-tenant-%d-%d", time.Now().UnixNano(), siteAID)

	for _, rowSiteID := range []uint32{siteAID, siteBID} {
		if err := stack.store.Conn().Exec(stack.ctx,
			`INSERT INTO statnive.events_raw
			   (site_id, time, hostname, pathname, event_type, event_name, cookie_id, visitor_hash)
			 VALUES
			   (?, now(), ?, '/', 'pageview', 'pageview', ?, unhex('00000000000000000000000000000000'))`,
			rowSiteID, hostnameA, sharedHash,
		); err != nil {
			t.Fatalf("seed events_raw for site %d: %v", rowSiteID, err)
		}
	}

	// Synchronously verify both rows landed before the erase.
	var preCount uint64
	if err := stack.store.Conn().QueryRow(stack.ctx,
		`SELECT count() FROM statnive.events_raw WHERE cookie_id = ?`, sharedHash,
	).Scan(&preCount); err != nil {
		t.Fatalf("pre-erase count: %v", err)
	}

	if preCount != 2 {
		t.Fatalf("pre-erase count = %d, want 2 (collision fixture broken)", preCount)
	}

	// Run the erase against site A only.
	results, err := stack.privacyErase.EraseByCookieID(stack.ctx, siteAID, sharedHash)
	if err != nil {
		t.Fatalf("EraseByCookieID: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("no tables erased")
	}

	// `mutations_sync` is NOT set; poll with timeout for the merge.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var siteACount, siteBCount uint64

		row := stack.store.Conn().QueryRow(stack.ctx,
			`SELECT countIf(site_id = ?), countIf(site_id = ?) FROM statnive.events_raw WHERE cookie_id = ?`,
			siteAID, siteBID, sharedHash,
		)
		if scanErr := row.Scan(&siteACount, &siteBCount); scanErr != nil {
			t.Fatalf("post-erase scan: %v", scanErr)
		}

		if siteACount == 0 && siteBCount == 1 {
			// Site A erased; site B's row survived. Invariant holds.
			return
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Timed out — collect final state for diagnosis.
	var siteACount, siteBCount uint64

	_ = stack.store.Conn().QueryRow(stack.ctx,
		`SELECT countIf(site_id = ?), countIf(site_id = ?) FROM statnive.events_raw WHERE cookie_id = ?`,
		siteAID, siteBID, sharedHash,
	).Scan(&siteACount, &siteBCount)

	t.Fatalf("cross-tenant isolation invariant violated: site A count = %d (want 0), site B count = %d (want 1)",
		siteACount, siteBCount)
}

// TestDSAR_EraseRejectsZeroSiteID confirms the new errEraseEmptySiteID
// guard short-circuits before any mutation is dispatched.
func TestDSAR_EraseRejectsZeroSiteID(t *testing.T) {
	const siteID uint32 = 4446

	stack := newIntegrationStack(t, siteID, "stage4-erase-zero.example.com")
	defer stack.shutdown()

	_, err := stack.privacyErase.EraseByCookieID(stack.ctx, 0, "h:cafebabe")
	if err == nil {
		t.Fatalf("EraseByCookieID with siteID=0 must error")
	}
}

// TestDSAR_Access_ReturnsVisitorRows asserts the read path's
// cross-tenant scoping. Seeds two rows for the visitor's cookie hash
// + one row for a different cookie hash on the same site; asserts
// only the two matching rows come back. Also seeds a row on a second
// site with the same cookie hash and asserts cross-tenant scoping
// holds — same invariant as TestDSAR_CrossTenantIsolation, applied
// to the read path so the surface a visitor can see equals the
// surface they can erase.
func TestDSAR_Access_ReturnsVisitorRows(t *testing.T) {
	const (
		siteAID   uint32 = 4447
		siteBID   uint32 = 4448
		hostnameA        = "stage4-access-a.example.com"
		hostnameB        = "stage4-access-b.example.com"
	)

	stack := newIntegrationStack(t, siteAID, hostnameA)
	defer stack.shutdown()

	// Seed site B so the cross-tenant assertion below has a real
	// neighbour.
	if err := stack.store.Conn().Exec(stack.ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (?, ?, ?, 1)`,
		siteBID, hostnameB, hostnameB,
	); err != nil {
		t.Fatalf("seed site B: %v", err)
	}

	// Per-run unique hashes so parallel test runs against a shared CH
	// don't collide.
	stamp := time.Now().UnixNano()
	visitorHash := fmt.Sprintf("h:test-access-visitor-%d", stamp)
	otherHash := fmt.Sprintf("h:test-access-other-%d", stamp)

	// Two rows for visitorHash on site A — these MUST appear in the
	// export.
	for i := range 2 {
		if err := stack.store.Conn().Exec(stack.ctx,
			`INSERT INTO statnive.events_raw
			   (site_id, time, hostname, pathname, event_type, event_name, cookie_id, visitor_hash, country_code)
			 VALUES
			   (?, now(), ?, ?, 'pageview', 'pageview', ?, unhex('00000000000000000000000000000000'), 'DE')`,
			siteAID, hostnameA, fmt.Sprintf("/page-%d", i), visitorHash,
		); err != nil {
			t.Fatalf("seed visitor row %d: %v", i, err)
		}
	}

	// One row for a different cookie on site A — MUST NOT appear.
	if err := stack.store.Conn().Exec(stack.ctx,
		`INSERT INTO statnive.events_raw
		   (site_id, time, hostname, pathname, event_type, event_name, cookie_id, visitor_hash)
		 VALUES
		   (?, now(), ?, '/other', 'pageview', 'pageview', ?, unhex('00000000000000000000000000000000'))`,
		siteAID, hostnameA, otherHash,
	); err != nil {
		t.Fatalf("seed other-visitor row: %v", err)
	}

	// One row for the SAME cookie hash on site B — MUST NOT appear in
	// the site-A export. Cross-tenant invariant for the read path.
	if err := stack.store.Conn().Exec(stack.ctx,
		`INSERT INTO statnive.events_raw
		   (site_id, time, hostname, pathname, event_type, event_name, cookie_id, visitor_hash)
		 VALUES
		   (?, now(), ?, '/leaked', 'pageview', 'pageview', ?, unhex('00000000000000000000000000000000'))`,
		siteBID, hostnameB, visitorHash,
	); err != nil {
		t.Fatalf("seed cross-tenant row: %v", err)
	}

	result, err := stack.privacyExport.ExportVisitorRows(stack.ctx, siteAID, visitorHash)
	if err != nil {
		t.Fatalf("ExportVisitorRows: %v", err)
	}

	if result.SiteID != siteAID {
		t.Errorf("SiteID = %d, want %d", result.SiteID, siteAID)
	}

	if result.CookieIDHash != visitorHash {
		t.Errorf("CookieIDHash = %q, want %q", result.CookieIDHash, visitorHash)
	}

	if result.RowCount != 2 {
		t.Errorf("RowCount = %d, want 2 (got rows: %+v)", result.RowCount, result.Rows)
	}

	if result.Truncated {
		t.Errorf("Truncated = true, want false (only 2 rows seeded)")
	}

	if result.GeneratedAt.IsZero() {
		t.Errorf("GeneratedAt is zero")
	}

	// Every returned row MUST come from site A's seed set. Detects
	// either the cross-tenant leak (siteB rows) or the wrong-cookie
	// leak (otherHash row).
	for _, r := range result.Rows {
		if r.Hostname != hostnameA {
			t.Errorf("row hostname = %q, want %q (cross-tenant leak)", r.Hostname, hostnameA)
		}

		if r.Pathname == "/other" {
			t.Errorf("row pathname = /other (wrong-cookie row leaked)")
		}

		if r.Pathname == "/leaked" {
			t.Errorf("row pathname = /leaked (cross-tenant row leaked)")
		}
	}
}

// TestDSAR_Erase_EmitsCompletedEvent asserts the goroutine spawned
// by the erase handler polls system.mutations and emits
// privacy.dsar_erase_completed after the ALTER ... DELETE lands.
// Pre-fix the audit log only carried privacy.dsar_erase_requested,
// leaving Art. 5(2) DSGVO accountability unfalsifiable from the
// audit file alone.
func TestDSAR_Erase_EmitsCompletedEvent(t *testing.T) {
	const (
		siteID   uint32 = 4451
		hostname        = "stage4-erase-completed.example.com"
	)

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	// Seed one row so the erase has real work to do — guarantees a
	// system.mutations row appears with create_time >= dispatchedAt.
	stamp := time.Now().UnixNano()
	visitorCookie := fmt.Sprintf("erase-completed-%d", stamp)
	visitorHash := identity.HexCookieIDHash(stack.masterKey, siteID, visitorCookie)

	if err := stack.store.Conn().Exec(stack.ctx,
		`INSERT INTO statnive.events_raw
		   (site_id, time, hostname, pathname, event_type, event_name, cookie_id, visitor_hash)
		 VALUES
		   (?, now(), ?, '/', 'pageview', 'pageview', ?, unhex('00000000000000000000000000000000'))`,
		siteID, hostname, visitorHash,
	); err != nil {
		t.Fatalf("seed events_raw: %v", err)
	}

	// Fire the erase via the HTTP route so the handler's
	// spawnCompletionWatcher goroutine actually runs (calling
	// EraseByCookieID directly bypasses the audit-event path).
	resp := postJSON(t, stack.srv, "/api/privacy/erase", "{}", []*http.Cookie{
		{Name: "_statnive", Value: visitorCookie},
	}, hostname)

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("erase status = %d, want 202", resp.StatusCode)
	}

	// The completion event lands once the CH mutation finishes. On a
	// fresh docker CH a single-row DELETE finishes in <500ms; 15s
	// gives plenty of headroom for CI noise.
	if !audittest.WaitForEvent(t, stack.auditPath, string(audit.EventDSAREraseCompleted), 15*time.Second) {
		t.Fatalf("dsar_erase_completed event not emitted within 15s — got events: %v",
			audittest.ReadEventNames(t, stack.auditPath))
	}

	// All three privacy events MUST appear in order. Timeout MUST NOT.
	events := audittest.ReadEventNames(t, stack.auditPath)

	requested := false
	completed := false

	for _, name := range events {
		switch name {
		case string(audit.EventDSAREraseRequested):
			requested = true
		case string(audit.EventDSAREraseCompleted):
			completed = true
		case string(audit.EventDSAREraseTimeout):
			t.Errorf("unexpected dsar_erase_timeout event — mutation should have completed")
		}
	}

	if !requested {
		t.Errorf("missing privacy.dsar_erase_requested event in %v", events)
	}

	if !completed {
		t.Errorf("missing privacy.dsar_erase_completed event in %v", events)
	}
}

// TestDSAR_Access_RejectsEmptyHash and TestDSAR_Access_RejectsZeroSiteID
// confirm the guards in ExportVisitorRows short-circuit before any CH
// query — same pattern as TestDSAR_EraseRejectsZeroSiteID.
func TestDSAR_Access_RejectsEmptyHash(t *testing.T) {
	const siteID uint32 = 4449

	stack := newIntegrationStack(t, siteID, "stage4-access-empty-hash.example.com")
	defer stack.shutdown()

	if _, err := stack.privacyExport.ExportVisitorRows(stack.ctx, siteID, ""); err == nil {
		t.Fatalf("ExportVisitorRows with empty hash must error")
	}
}

func TestDSAR_Access_RejectsZeroSiteID(t *testing.T) {
	const siteID uint32 = 4450

	stack := newIntegrationStack(t, siteID, "stage4-access-zero-site.example.com")
	defer stack.shutdown()

	if _, err := stack.privacyExport.ExportVisitorRows(stack.ctx, 0, "h:cafebabe"); err == nil {
		t.Fatalf("ExportVisitorRows with siteID=0 must error")
	}
}

// TestSaltRotation_PerSiteTimezone verifies the v0.0.39 wiring:
// LookupSitePolicy reads statnive.sites.tz and surfaces it in
// SitePolicy.TZ, which the pipeline then passes to Salt.CurrentSalt.
// Confirms two sites with different tz produce different visitor
// hashes for the same IP+UA at the same UTC moment.
//
// Backs the operator-side gate from
// televika-PATH-A-IMPLEMENTATION.md Section 13 signal #1.
func TestSaltRotation_PerSiteTimezone(t *testing.T) {
	const (
		siteATehran uint32 = 5101
		siteBBerlin uint32 = 5102
		hostnameA          = "tz-rotation-a.example.com"
		hostnameB          = "tz-rotation-b.example.com"
	)

	stack := newIntegrationStack(t, siteATehran, hostnameA)
	defer stack.shutdown()

	// Seed site B with a different tz so the dual-site assertion has
	// something to compare against. Insert directly because the admin
	// PATCH path isn't wired into this test harness.
	if err := stack.store.Conn().Exec(stack.ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, enabled, tz)
		 VALUES (?, ?, ?, 1, 'Europe/Berlin')`,
		siteBBerlin, hostnameB, hostnameB,
	); err != nil {
		t.Fatalf("seed site B: %v", err)
	}

	// Force site A to Asia/Tehran (the seed leaves it at whatever the
	// migration-021 default surfaces).
	if err := stack.store.Conn().Exec(stack.ctx,
		`ALTER TABLE statnive.sites UPDATE tz = 'Asia/Tehran' WHERE site_id = ?`,
		siteATehran,
	); err != nil {
		t.Fatalf("force site A tz=Asia/Tehran: %v", err)
	}

	// Construct a fresh Registry + SaltManager from the stack's CH
	// connection. The stack itself doesn't expose these as fields, but
	// constructing them locally is a few lines and avoids harness churn.
	registry := sites.New(stack.store.Conn())

	saltMgr, err := identity.NewSaltManager([]byte(stage4MasterSecret))
	if err != nil {
		t.Fatalf("NewSaltManager: %v", err)
	}

	// Wait for the ALTER mutation to settle.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, policy, lookupErr := registry.LookupSitePolicy(stack.ctx, hostnameA)
		if lookupErr == nil && policy.TZ == "Asia/Tehran" {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Confirm both sites return the expected tz via the policy lookup.
	_, policyA, err := registry.LookupSitePolicy(stack.ctx, hostnameA)
	if err != nil {
		t.Fatalf("LookupSitePolicy hostnameA: %v", err)
	}

	if policyA.TZ != "Asia/Tehran" {
		t.Errorf("site A policy.TZ = %q, want %q", policyA.TZ, "Asia/Tehran")
	}

	_, policyB, err := registry.LookupSitePolicy(stack.ctx, hostnameB)
	if err != nil {
		t.Fatalf("LookupSitePolicy hostnameB: %v", err)
	}

	if policyB.TZ != "Europe/Berlin" {
		t.Errorf("site B policy.TZ = %q, want %q", policyB.TZ, "Europe/Berlin")
	}

	// Pick a moment where Asia/Tehran and Europe/Berlin land on
	// different YYYY-MM-DD values: 21:00 UTC = 00:30 IRST next day,
	// 23:00 CEST (summer) / 22:00 CET (winter) same day.
	wallClock := time.Date(2026, 4, 17, 21, 0, 0, 0, time.UTC)
	saltMgr.SetClock(func() time.Time { return wallClock })

	saltTehran := saltMgr.CurrentSalt(siteATehran, policyA.TZ)
	saltBerlin := saltMgr.CurrentSalt(siteBBerlin, policyB.TZ)

	if saltTehran == saltBerlin {
		t.Errorf("per-site rotation broken: Tehran and Berlin sites produced identical salt at the local-day seam\n  Tehran: %s\n  Berlin: %s",
			saltTehran, saltBerlin)
	}
}

// TestMigration_021_SitesTzDefaultUTC confirms migration 021 ran and
// the sites.tz column default is now 'UTC'. Existing rows are
// unaffected — the migration only flips the column DEFAULT clause.
func TestMigration_021_SitesTzDefaultUTC(t *testing.T) {
	const siteID uint32 = 5103

	stack := newIntegrationStack(t, siteID, "tz-default-utc.example.com")
	defer stack.shutdown()

	// Read the column's current default from system.columns.
	var defaultExpr string
	if err := stack.store.Conn().QueryRow(stack.ctx,
		`SELECT default_expression FROM system.columns
		 WHERE database = ? AND table = 'sites' AND name = 'tz' LIMIT 1`,
		testDatabase,
	).Scan(&defaultExpr); err != nil {
		t.Fatalf("read sites.tz column default: %v", err)
	}

	// ClickHouse returns the DEFAULT expression as a string literal
	// like `'UTC'`. After migration 021 it should be 'UTC', not the
	// migration-003 'Asia/Tehran'.
	if defaultExpr != "'UTC'" && defaultExpr != "UTC" {
		t.Errorf("sites.tz default = %q, want 'UTC' (migration 021 not applied or reverted)", defaultExpr)
	}
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
