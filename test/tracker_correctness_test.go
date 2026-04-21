//go:build integration

// Tracker payload-golden correctness — Phase 7b2.
//
// Reads tracker/test/payload-golden.test.mjs's emitted golden at
// test/fixtures/tracker-payloads.json (the EXACT bytes the JS tracker
// writes via sendBeacon), POSTs each payload through the full
// handler → enrichment pipeline → GroupSyncer → consumer → ClickHouse
// stack, then asserts every payload landed in events_raw.
//
// This is the only test that proves the cross-language tracker → server
// contract end-to-end without needing a real browser. If the JS tracker
// drifts from the Go RawEvent JSON tags, this test fails.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/statnive/statnive.live/internal/storage"
)

const (
	trackerSiteID   = uint32(303)
	trackerHostname = "tracker-correctness.example.com"
)

type trackerGoldenEntry struct {
	Name string                 `json:"name"`
	Body map[string]interface{} `json:"body"`
}

func TestTrackerCorrectness_PayloadsLandInClickHouse(t *testing.T) {
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

	if cleanErr := store.Conn().Exec(ctx,
		`ALTER TABLE statnive.events_raw DELETE WHERE site_id = ? SETTINGS mutations_sync = 2`,
		trackerSiteID,
	); cleanErr != nil {
		t.Logf("clean (ok on first run): %v", cleanErr)
	}

	if seedErr := store.Conn().Exec(ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (?, ?, ?, 1)`,
		trackerSiteID, trackerHostname, "tracker-correctness",
	); seedErr != nil {
		t.Fatalf("seed site: %v", seedErr)
	}

	// Read the JS-emitted golden — same bytes the tracker writes via sendBeacon.
	goldenPath := filepath.Join("fixtures", "tracker-payloads.json")

	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v", goldenPath, err)
	}

	var golden []trackerGoldenEntry
	if jsonErr := json.Unmarshal(goldenBytes, &golden); jsonErr != nil {
		t.Fatalf("decode golden: %v", jsonErr)
	}

	if len(golden) == 0 {
		t.Fatal("golden is empty; regenerate via `cd tracker && npm test`")
	}

	srv, _ := newTestPipelineServer(t, ctx, store, logger)

	client := &http.Client{Timeout: testHTTPTimeout}

	for i, entry := range golden {
		// Copy the map so per-iteration overrides don't pollute the in-memory
		// golden across iterations (entry.Body is a reference type).
		mutated := make(map[string]interface{}, len(entry.Body)+2)
		for k, v := range entry.Body {
			mutated[k] = v
		}

		mutated["hostname"] = trackerHostname
		mutated["pathname"] = fmt.Sprintf("/tracker-golden/%s/%d", entry.Name, i)

		body, mErr := json.Marshal(mutated)
		if mErr != nil {
			t.Fatalf("re-encode %s: %v", entry.Name, mErr)
		}

		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/event", bytes.NewReader(body))
		if reqErr != nil {
			t.Fatalf("request %s: %v", entry.Name, reqErr)
		}

		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
		// Unique X-Forwarded-For so each event gets a distinct visitor_hash.
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", 100+i))

		resp, doErr := client.Do(req)
		if doErr != nil {
			t.Fatalf("POST %s: %v", entry.Name, doErr)
		}

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST %s status = %d; want 202", entry.Name, resp.StatusCode)
		}

		_ = resp.Body.Close()
	}

	// Wait for batcher + CH ingest. Reuse the existing helper.
	waitForCount(t, ctx, store, trackerSiteID, len(golden), flushTimeout)

	// Belt-and-braces: confirm an identified event landed with the hashed
	// user_id (tracker sent raw "user_phase7b2_42"; pipeline must hash it
	// before write — Privacy Rule 4).
	var hashedRows uint64

	row := store.Conn().QueryRow(ctx,
		`SELECT count() FROM statnive.events_raw
		 WHERE site_id = ? AND user_id != '' AND length(user_id) = 64`,
		trackerSiteID,
	)
	if scanErr := row.Scan(&hashedRows); scanErr != nil {
		t.Fatalf("count hashed user_id: %v", scanErr)
	}

	if hashedRows == 0 {
		t.Errorf("no rows with hashed user_id; expected at least 1 from the identified payload")
	}

	// Rollup correctness has its own dedicated unit tests. Skip the
	// hourly_visitors check here — the MV update lag would force either a
	// flaky sleep or a long poll, and events_raw above is the load-bearing
	// proof that the tracker → server contract holds.
}
