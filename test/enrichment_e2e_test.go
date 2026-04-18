//go:build integration

// End-to-end smoke for the 6-stage enrichment pipeline. Sends three
// distinct events through the real pipeline + ClickHouse and asserts the
// rows that land in events_raw carry the expected per-stage outputs:
//   - identity: visitor_hash is non-zero (BLAKE3, not the Phase 0 zero stub)
//   - bloom:    is_new = 1 on first sighting, 0 on second
//   - geo:      country_code = "--" (no DB configured) — proves no-op path works
//   - ua:       browser/os/device populated for a real UA
//   - bot:      is_bot = 1 for Googlebot, 0 for Chrome
//   - channel:  channel = "AI" for chat.openai.com referrer
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
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/enrich"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
)

const (
	enrichSiteID   = 84
	enrichHostname = "enrichment-e2e.example.com"
)

func TestEnrichmentPipelineEndToEnd(t *testing.T) {
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

	_ = store.Conn().Exec(ctx,
		`ALTER TABLE statnive.events_raw DELETE WHERE site_id = ? SETTINGS mutations_sync = 2`,
		uint32(enrichSiteID),
	)

	if err := store.Conn().Exec(ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (?, ?, ?, 1)`,
		uint32(enrichSiteID), enrichHostname, "enrichment-e2e",
	); err != nil {
		t.Fatalf("seed site: %v", err)
	}

	srv, _ := newTestPipelineServer(t, ctx, store, logger)

	// Three events with different shapes to exercise the stages.
	events := []ingest.RawEvent{
		{
			Hostname:  enrichHostname,
			Pathname:  "/page-1",
			EventType: "pageview",
			EventName: "pageview",
			Referrer:  "https://chat.openai.com/",
		},
		{
			Hostname:  enrichHostname,
			Pathname:  "/page-2",
			EventType: "pageview",
			EventName: "pageview",
			Referrer:  "https://chat.openai.com/",
		},
		{
			Hostname:  enrichHostname,
			Pathname:  "/page-3",
			EventType: "pageview",
			EventName: "pageview",
		},
	}

	uas := []string{
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120",
		"Googlebot/2.1 (+http://www.google.com/bot.html)",
	}

	client := &http.Client{Timeout: testHTTPTimeout}

	for i, ev := range events {
		body, _ := json.Marshal(ev)

		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/event", bytes.NewReader(body))
		req.Header.Set("User-Agent", uas[i])
		req.Header.Set("Content-Type", "text/plain")

		resp, doErr := client.Do(req)
		if doErr != nil {
			t.Fatalf("POST %d: %v", i, doErr)
		}

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST %d status = %d", i, resp.StatusCode)
		}

		_ = resp.Body.Close()
	}

	waitForCount(t, ctx, store, enrichSiteID, len(events), flushTimeout)

	// Inspect the rows. Order by pathname so assertions are stable.
	type row struct {
		Pathname    string
		Channel     string
		IsBot       uint8
		IsNew       uint8
		Browser     string
		Device      string
		CountryCode string
		VisitorHex  string
	}

	rows, err := store.Conn().Query(ctx, `
		SELECT pathname, channel, is_bot, is_new, browser, device_type, country_code, hex(visitor_hash)
		FROM statnive.events_raw
		WHERE site_id = ?
		ORDER BY pathname
	`, uint32(enrichSiteID))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var got []row

	for rows.Next() {
		var r row
		if scanErr := rows.Scan(&r.Pathname, &r.Channel, &r.IsBot, &r.IsNew, &r.Browser, &r.Device, &r.CountryCode, &r.VisitorHex); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}

		got = append(got, r)
	}

	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}

	// /page-1: Chrome from ChatGPT referrer.
	r0 := got[0]
	assertChannel(t, r0.Pathname, r0.Channel, "AI")
	assertBool(t, r0.Pathname+" is_new", r0.IsNew, 1)
	assertBool(t, r0.Pathname+" is_bot", r0.IsBot, 0)
	if r0.Browser == "" {
		t.Errorf("/page-1: browser should be populated for Chrome UA, got empty")
	}
	if r0.Device != "desktop" {
		t.Errorf("/page-1: device = %q, want desktop", r0.Device)
	}
	if r0.VisitorHex == "00000000000000000000000000000000" {
		t.Errorf("/page-1: visitor_hash is zero — BLAKE3 not running")
	}

	// /page-2: same visitor as /page-1 → returning.
	r1 := got[1]
	assertBool(t, r1.Pathname+" is_new", r1.IsNew, 0)

	// /page-3: Googlebot UA → flagged as bot.
	r2 := got[2]
	assertBool(t, r2.Pathname+" is_bot", r2.IsBot, 1)

	// All rows: country_code = "--" because no GeoIP DB is configured.
	for _, r := range got {
		if r.CountryCode != "--" {
			t.Errorf("%s: country_code = %q, want -- (no DB configured)", r.Pathname, r.CountryCode)
		}
	}

	cancel()
}

func assertChannel(t *testing.T, label, got, want string) {
	t.Helper()

	if got != want {
		t.Errorf("%s: channel = %q, want %q", label, got, want)
	}
}

func assertBool(t *testing.T, label string, got, want uint8) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %d, want %d", label, got, want)
	}
}

// newTestPipelineServer wires the full pipeline + consumer + chi router
// against a real ClickHouse store. Returns the test server URL and a hook
// to wait on shutdown if the caller needs it.
func newTestPipelineServer(t *testing.T, ctx context.Context, store *storage.ClickHouseStore, logger *slog.Logger) (*httptest.Server, *enrich.Pipeline) {
	t.Helper()

	walDir := t.TempDir()

	wal, err := ingest.NewWALWriter(ingest.WALConfig{Dir: filepath.Join(walDir, "wal")}, logger)
	if err != nil {
		t.Fatalf("wal: %v", err)
	}
	t.Cleanup(func() { _ = wal.Close() })

	saltMgr, err := identity.NewSaltManager([]byte(fmt.Sprintf("test-secret-%s-32-bytes-padding", t.Name())))
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

	consumer := ingest.NewConsumer(pipeline.Out(), wal, store, ingest.ConsumerConfig{
		BatchRows:     50,
		BatchInterval: 100 * time.Millisecond,
	}, logger)

	go func() { _ = pipeline.Run(ctx) }()
	go consumer.Run(ctx)

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(ingest.FastRejectMiddleware(nil))
		r.Method(http.MethodPost, "/api/event", ingest.NewHandler(ingest.HandlerConfig{
			Pipeline: pipeline,
			Sites:    sites.New(store.Conn()),
			Logger:   logger,
		}))
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return srv, pipeline
}
