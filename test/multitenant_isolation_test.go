//go:build integration

// Multitenant isolation: pin Privacy Rule 2 — same (IP, UA) under
// different site_ids MUST produce different visitor_hashes because
// site_id is folded into the HMAC input that derives each tenant's
// daily salt. Without this property, deanonymization across tenant
// boundaries becomes possible.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/storage"
)

const (
	tenantA  = uint32(101)
	tenantB  = uint32(102)
	hostA    = "tenant-a.example.com"
	hostB    = "tenant-b.example.com"
	sharedUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/120 IsolationProbe"
)

func TestMultitenantVisitorHashSeparation(t *testing.T) {
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

	for _, sid := range []uint32{tenantA, tenantB} {
		// mutations_sync=2 makes the DELETE block until the merge applies
		// across all replicas — without it, leftover rows from previous
		// test runs leak into the current assertions.
		_ = store.Conn().Exec(ctx,
			`ALTER TABLE statnive.events_raw DELETE WHERE site_id = ? SETTINGS mutations_sync = 2`, sid,
		)
	}

	if err := store.Conn().Exec(ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (?, ?, ?, 1), (?, ?, ?, 1)`,
		tenantA, hostA, "tenant-a",
		tenantB, hostB, "tenant-b",
	); err != nil {
		t.Fatalf("seed sites: %v", err)
	}

	srv, _ := newTestPipelineServer(t, ctx, store, logger)

	client := &http.Client{Timeout: testHTTPTimeout}

	// Same UA fired against both hosts. Same X-Forwarded-For so the
	// pipeline sees identical (IP, UA) input. Only site_id differs.
	for _, host := range []string{hostA, hostB} {
		body, _ := json.Marshal(ingest.RawEvent{
			Hostname:  host,
			Pathname:  "/iso",
			EventType: "pageview",
			EventName: "pageview",
		})

		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/event", bytes.NewReader(body))
		req.Header.Set("User-Agent", sharedUA)
		req.Header.Set("X-Forwarded-For", "203.0.113.42")
		req.Header.Set("Content-Type", "text/plain")

		resp, doErr := client.Do(req)
		if doErr != nil {
			t.Fatalf("POST %s: %v", host, doErr)
		}

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST %s status = %d", host, resp.StatusCode)
		}

		_ = resp.Body.Close()
	}

	// Longer wait than flushTimeout because this test runs right after
	// TestIngestPipelineSmoke's 100-event drain and CI-side CH is
	// occasionally slow to process the first insert of a new pipeline.
	// Happy path still completes in <1s; 45s only protects against the
	// post-drain + consumer-spin-up latency ceiling.
	const multitenantWait = 45 * time.Second

	waitForCount(t, ctx, store, tenantA, 1, multitenantWait)
	waitForCount(t, ctx, store, tenantB, 1, multitenantWait)

	hashA := readVisitorHash(t, ctx, store, tenantA)
	hashB := readVisitorHash(t, ctx, store, tenantB)

	if hashA == "" || hashB == "" {
		t.Fatalf("missing hash: A=%q B=%q", hashA, hashB)
	}

	if hashA == hashB {
		t.Errorf("CRITICAL: same (IP,UA) under different site_ids produced identical visitor_hash %q — Privacy Rule 2 violated", hashA)
	}

	// Sanity: the cross-tenant SELECT also returns zero leakage.
	var crossCount uint64

	row := store.Conn().QueryRow(ctx,
		`SELECT count() FROM statnive.events_raw WHERE site_id = ? AND visitor_hash = unhex(?)`,
		tenantB, hashA,
	)
	if scanErr := row.Scan(&crossCount); scanErr != nil {
		t.Fatalf("cross-tenant query: %v", scanErr)
	}

	if crossCount != 0 {
		t.Errorf("CRITICAL: tenant A's visitor_hash %q leaked into tenant B query (count=%d)", hashA, crossCount)
	}

	cancel()
}

func readVisitorHash(t *testing.T, ctx context.Context, store *storage.ClickHouseStore, siteID uint32) string {
	t.Helper()

	var hex string

	row := store.Conn().QueryRow(ctx,
		`SELECT hex(visitor_hash) FROM statnive.events_raw WHERE site_id = ? LIMIT 1`,
		siteID,
	)
	if err := row.Scan(&hex); err != nil {
		t.Fatalf("read hash for site %d: %v", siteID, err)
	}

	return hex
}
