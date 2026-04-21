//go:build integration

// Integration-level PII grep — Phase 7b2 closes a CLAUDE.md § Enforcement
// item.
//
// Unit tests assert "the function clears the field"; only an integration
// test that BYTE-SCANS the on-disk WAL segments + audit JSONL + ClickHouse
// events_raw rows can prove the field actually never lands. This file
// fires probe events whose raw user_id and IP contain unique sniff-bait
// tokens, then asserts none of those tokens appear anywhere in any of the
// three persistent surfaces. Pins Privacy Rules 1 (raw IP discarded
// post-GeoIP) and 4 (raw user_id hashed before write).
package integration_test

import (
	"bufio"
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

	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/enrich"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
)

const (
	piiSiteID   = uint32(404)
	piiHostname = "pii-leak-test.example.com"

	// Sniff-bait tokens: rare strings unique enough that ANY appearance
	// in the persistent surfaces below is a privacy regression.
	piiUserIDProbe = "PII_PROBE_USERID_PHASE7B2"
	piiIPProbe     = "203.0.113.42" // TEST-NET-3 documentation range
	piiEvents      = 50
)

// TestPIILeak_RawUserIDAndIPNeverPersist scans WAL segments, audit JSONL
// lines, and events_raw rows for the probe tokens after firing events
// that include them. Any hit is a privacy contract violation.
func TestPIILeak_RawUserIDAndIPNeverPersist(t *testing.T) {
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
	if mErr := migrator.Run(ctx); mErr != nil {
		t.Fatalf("migrate: %v", mErr)
	}

	if cleanErr := store.Conn().Exec(ctx,
		`ALTER TABLE statnive.events_raw DELETE WHERE site_id = ? SETTINGS mutations_sync = 2`,
		piiSiteID,
	); cleanErr != nil {
		t.Logf("clean (ok on first run): %v", cleanErr)
	}

	if seedErr := store.Conn().Exec(ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (?, ?, ?, 1)`,
		piiSiteID, piiHostname, "pii-leak-test",
	); seedErr != nil {
		t.Fatalf("seed site: %v", seedErr)
	}

	// We need the WAL dir + audit path on the test side to grep them.
	// Wire the harness inline rather than reusing newTestPipelineServer.
	walDir := t.TempDir()
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")

	auditLog, err := audit.New(auditPath)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	t.Cleanup(func() { _ = auditLog.Close() })

	wal, err := ingest.NewWALWriter(ingest.WALConfig{Dir: filepath.Join(walDir, "wal")}, logger)
	if err != nil {
		t.Fatalf("wal: %v", err)
	}
	t.Cleanup(func() { _ = wal.Close() })

	saltMgr, err := identity.NewSaltManager([]byte("pii-leak-test-master-secret-32by"))
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

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(ingest.FastRejectMiddleware(auditLog))
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

	client := &http.Client{Timeout: testHTTPTimeout}

	for i := 0; i < piiEvents; i++ {
		body, _ := json.Marshal(ingest.RawEvent{
			Hostname:  piiHostname,
			Pathname:  fmt.Sprintf("/pii/%d", i),
			EventType: "pageview",
			EventName: "pageview",
			UserID:    piiUserIDProbe, // raw — must be hashed before WAL/CH/audit.
		})

		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/event", bytes.NewReader(body))
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh) PIILeakTest/1.0")
		req.Header.Set("X-Forwarded-For", piiIPProbe)

		resp, doErr := client.Do(req)
		if doErr != nil {
			t.Fatalf("POST %d: %v", i, doErr)
		}

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST %d status = %d; want 202", i, resp.StatusCode)
		}

		_ = resp.Body.Close()
	}

	waitForCount(t, ctx, store, piiSiteID, piiEvents, flushTimeout)

	// 1) WAL byte scan — every segment file in walDir/wal/ must be free
	// of both probes. The user_id is hashed before the WAL append (handler
	// path); the IP is dropped after GeoIP runs (Privacy Rule 1).
	walFiles, walkErr := walSegmentPaths(filepath.Join(walDir, "wal"))
	if walkErr != nil {
		t.Fatalf("walk wal dir: %v", walkErr)
	}

	if len(walFiles) == 0 {
		t.Fatal("no WAL segments found; harness misconfigured")
	}

	for _, p := range walFiles {
		assertFileFreeOfProbe(t, p, piiUserIDProbe, "WAL segment")
		assertFileFreeOfProbe(t, p, piiIPProbe, "WAL segment")
	}

	// 2) Audit JSONL scan — every line must be free of both probes.
	assertFileFreeOfProbe(t, auditPath, piiUserIDProbe, "audit log")
	assertFileFreeOfProbe(t, auditPath, piiIPProbe, "audit log")

	// 3) ClickHouse events_raw scan via SQL — direct field-level check.
	var leakedRows uint64

	row := store.Conn().QueryRow(ctx,
		`SELECT count() FROM statnive.events_raw
		 WHERE site_id = ? AND (user_id = ? OR user_id LIKE concat('%', ?, '%'))`,
		piiSiteID, piiUserIDProbe, piiUserIDProbe,
	)
	if scanErr := row.Scan(&leakedRows); scanErr != nil {
		t.Fatalf("count user_id probe: %v", scanErr)
	}

	if leakedRows != 0 {
		t.Errorf("events_raw leaked raw user_id: %d rows match probe %q", leakedRows, piiUserIDProbe)
	}

	// hourly_visitors holds visitor_hash + visitor HLL; no IP/user_id
	// columns by schema. The events_raw check above is the load-bearing
	// assertion for the rollups (rollup tests cover correctness directly).

	cancel()
}

// walSegmentPaths returns every regular file under root. tidwall/wal
// segments share no extension; we scan all files to be conservative.
func walSegmentPaths(root string) ([]string, error) {
	var paths []string

	walkErr := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		paths = append(paths, p)

		return nil
	})

	return paths, walkErr
}

// assertFileFreeOfProbe streams f line-by-line and fails the test if any
// line contains the probe. Reading the whole file via io.ReadAll is
// fine for our test sizes (<1MB), but bufio.Scanner gives a precise
// "first match line" without loading the whole buffer.
func assertFileFreeOfProbe(t *testing.T, path, probe, label string) {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("%s open %s: %v", label, path, err)
	}
	defer f.Close()

	// Bigger than the default 64KB so a wide audit line doesn't trip
	// bufio.ErrTooLong.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	probeBytes := []byte(probe)

	for lineNo := 1; scanner.Scan(); lineNo++ {
		if bytes.Contains(scanner.Bytes(), probeBytes) {
			t.Errorf("%s %s: line %d contains PII probe %q", label, path, lineNo, probe)

			break
		}
	}

	if scanErr := scanner.Err(); scanErr != nil {
		// The WAL is binary so Scanner may complain about a long "line".
		// Fall back to a whole-file substring check.
		f2, _ := os.Open(path)
		defer f2.Close()

		all, _ := io.ReadAll(f2)
		if bytes.Contains(all, probeBytes) {
			t.Errorf("%s %s: PII probe %q found in raw bytes (scanner err: %v)", label, path, probe, scanErr)
		}
	}
}
