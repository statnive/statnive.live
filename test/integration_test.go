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
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/enrich"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
)

const (
	testHostname    = "integration-test.example.com"
	testSiteID      = 42
	eventCount      = 100
	flushTimeout    = 5 * time.Second
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
	if cleanErr := store.Conn().Exec(ctx,
		`ALTER TABLE statnive.events_raw DELETE WHERE site_id = ?`, uint32(testSiteID),
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

	out := make(chan ingest.EnrichedEvent, eventCount*2)
	consumer := ingest.NewConsumer(out, wal, store, ingest.ConsumerConfig{
		BatchRows:     50,
		BatchInterval: 100 * time.Millisecond,
	}, logger)

	consumerDone := make(chan struct{})
	go func() {
		consumer.Run(ctx)
		close(consumerDone)
	}()

	router := chi.NewRouter()
	router.Method(http.MethodPost, "/api/event", ingest.NewHandler(ingest.HandlerConfig{
		Pipeline: enrich.NewStub(identity.NewHasherStub()),
		Sites:    sites.New(store.Conn()),
		Out:      out,
		Logger:   logger,
	}))

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

	// Shutdown drains any in-flight batches.
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
