//go:build integration

// Security integration test — proves the rate-limit middleware
// short-circuits at the HTTP layer, before the handler even resolves
// the hostname against ClickHouse. Without this gate, an abusive
// client could drain WAL + ClickHouse capacity at line speed.
//
// Per-package security unit tests live under internal/{audit,cert,
// ratelimit}/. This file covers the cross-cutting handler↔ratelimit↔
// audit integration that those unit tests can't observe.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/audit/audittest"
	"github.com/statnive/statnive.live/internal/enrich"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/ratelimit"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
)

const (
	securitySiteID   = uint32(202)
	securityHostname = "security-test.example.com"
	rateLimitCap     = 5 // requests per minute for this test
)

func TestSecurity_RateLimitShortCircuitsBeforeIngest(t *testing.T) {
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

	if err := store.Conn().Exec(ctx,
		`ALTER TABLE statnive.events_raw DELETE WHERE site_id = ? SETTINGS mutations_sync = 2`,
		securitySiteID,
	); err != nil {
		t.Logf("clean (ok on first run): %v", err)
	}

	if err := store.Conn().Exec(ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (?, ?, ?, 1)`,
		securitySiteID, securityHostname, "security-test",
	); err != nil {
		t.Fatalf("seed site: %v", err)
	}

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")

	auditLog, err := audit.New(auditPath)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	t.Cleanup(func() { _ = auditLog.Close() })

	srv := newRateLimitedTestServer(t, ctx, store, auditLog, logger, rateLimitCap)

	const totalRequests = 25

	statuses := make(map[int]int)

	client := &http.Client{Timeout: testHTTPTimeout}

	for i := 0; i < totalRequests; i++ {
		body, _ := json.Marshal(ingest.RawEvent{
			Hostname:  securityHostname,
			Pathname:  "/security",
			EventType: "pageview",
			EventName: "pageview",
		})

		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/event", bytes.NewReader(body))
		req.Header.Set("User-Agent", "Mozilla/5.0 (SecurityTest/1.0) BrowserLike")
		req.Header.Set("X-Forwarded-For", "203.0.113.99")
		req.Header.Set("Content-Type", "text/plain")

		resp, doErr := client.Do(req)
		if doErr != nil {
			t.Fatalf("POST %d: %v", i, doErr)
		}

		statuses[resp.StatusCode]++
		_ = resp.Body.Close()
	}

	if statuses[http.StatusAccepted] == 0 {
		t.Errorf("expected some 202s; got %v", statuses)
	}

	if statuses[http.StatusTooManyRequests] == 0 {
		t.Errorf("expected some 429s after burst; got %v", statuses)
	}

	if accepted := statuses[http.StatusAccepted]; accepted > rateLimitCap {
		t.Errorf("rate limit leaked: %d 202s exceed cap of %d", accepted, rateLimitCap)
	}

	// Wait for the in-flight pipeline + consumer to flush. Then assert
	// ClickHouse only sees the events the rate limiter let through.
	waitForCount(t, ctx, store, securitySiteID, statuses[http.StatusAccepted], flushTimeout)

	if err := auditLog.Close(); err != nil {
		t.Fatalf("audit close: %v", err)
	}

	events := audittest.ReadEventNames(t, auditPath)

	rateLimitedCount := 0
	for _, e := range events {
		if e == string(audit.EventRateLimited) {
			rateLimitedCount++
		}
	}

	if rateLimitedCount == 0 {
		t.Errorf("expected ratelimit.exceeded events; got: %v", events)
	}

	cancel()
}

// newRateLimitedTestServer wires the same chi-middleware stack main.go
// uses, so the integration test exercises the production path (rate
// limit + handler + audit + pipeline + consumer + ClickHouse).
func newRateLimitedTestServer(
	t *testing.T,
	ctx context.Context,
	store *storage.ClickHouseStore,
	auditLog *audit.Logger,
	logger *slog.Logger,
	rps int,
) *httptest.Server {
	t.Helper()

	walDir := t.TempDir()

	wal, err := ingest.NewWALWriter(ingest.WALConfig{Dir: filepath.Join(walDir, "wal")}, logger)
	if err != nil {
		t.Fatalf("wal: %v", err)
	}
	t.Cleanup(func() { _ = wal.Close() })

	saltMgr, err := identity.NewSaltManager([]byte("security-test-master-secret-32by"))
	if err != nil {
		t.Fatalf("salt: %v", err)
	}

	geoIP, _ := enrich.NewGeoIPEnricher("", logger)
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

	groupSyncer := ingest.NewGroupSyncer(wal, ingest.GroupConfig{}, auditLog, logger)
	t.Cleanup(groupSyncer.Close)

	consumer := ingest.NewConsumer(groupSyncer.Out(), wal, store, ingest.ConsumerConfig{
		BatchRows:     50,
		BatchInterval: 100 * time.Millisecond,
		DrainSettle:   100 * time.Millisecond,
	}, auditLog, logger)

	go consumer.Run(ctx)

	rateLimitMW, err := ratelimit.Middleware(rps, time.Minute, auditLog)
	if err != nil {
		t.Fatalf("ratelimit: %v", err)
	}

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(ingest.FastRejectMiddleware(auditLog))
		r.Use(rateLimitMW)
		r.Method(http.MethodPost, "/api/event", ingest.NewHandler(ingest.HandlerConfig{
			Pipeline: pipeline,
			WAL:      groupSyncer,
			Sites:    sites.New(store.Conn()),
			Audit:    auditLog,
			Logger:   logger,
		}))
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return srv
}

