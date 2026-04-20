//go:build slow

// ClickHouse outage test: while events are flowing through the binary,
// stop the CH container, keep firing for ~10s (events buffer to WAL),
// restart CH, assert the WAL drains. The full 10-min outage scenario
// from PLAN.md verification 10 stays manual (CI containers don't
// tolerate a 10-min stop); this test exercises the same code path on a
// shorter timer to fit into CI/dev cycles.
package perf

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/storage"
	"github.com/statnive/statnive.live/internal/storage/storagetest"
)

const (
	outageSiteID    = uint32(702)
	outageHostname  = "ch-outage.example.com"
	outagePreEvents = 2000 // before stopping CH
	outageGapEvents = 2000 // while CH is down
	outageRate      = 500  // events/sec
	chContainerName = "statnive-clickhouse-dev"
)

func TestCHOutage_BufferAndDrain(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not available: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs:    []string{CHAddr},
		Database: "statnive",
		Username: "default",
	}, logger)
	if err != nil {
		t.Skipf("integration: clickhouse ping failed, skipping: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	migrator := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{Database: "statnive"}, logger)
	if migErr := migrator.Run(ctx); migErr != nil {
		t.Fatalf("migrate: %v", migErr)
	}

	storagetest.CleanSiteEvents(t, ctx, store.Conn(), outageSiteID)
	storagetest.SeedSite(t, ctx, store.Conn(), outageSiteID, outageHostname)

	// Close our own conn before stopping CH — keeping the conn open
	// during a stop logs noisy "connection refused" lines from the
	// driver. The waitForCount helper opens a fresh conn after restart.
	_ = store.Close()

	bin := BinaryPath(t)
	walDir := t.TempDir()
	masterKey := writeMasterKey(t, walDir)

	env := []string{
		"STATNIVE_SERVER_LISTEN=" + PerfHTTPAddr,
		"STATNIVE_CLICKHOUSE_ADDR=" + CHAddr,
		"STATNIVE_INGEST_WAL_DIR=" + filepath.Join(walDir, "wal"),
		"STATNIVE_AUDIT_PATH=" + filepath.Join(walDir, "audit.jsonl"),
		"STATNIVE_MASTER_SECRET_PATH=" + masterKey,
		"STATNIVE_RATELIMIT_REQUESTS_PER_MINUTE=120000",
	}

	_, cleanup := SpawnBinary(t, ctx, bin, env)
	defer cleanup()

	// Phase 1: fire events with CH up.
	pre := FireEvents(t, ctx, outageHostname, outagePreEvents, outageRate)
	t.Logf("phase 1 (CH up): sent %d events", pre)

	// Phase 2: stop CH, keep firing — events should buffer to the WAL.
	if err := DockerCommand("stop", chContainerName); err != nil {
		t.Fatalf("docker stop: %v", err)
	}
	t.Logf("CH stopped; firing %d events into WAL", outageGapEvents)

	gap := FireEvents(t, ctx, outageHostname, outageGapEvents, outageRate)
	t.Logf("phase 2 (CH down): sent %d events", gap)

	// Phase 3: restart CH; consumer should drain the WAL backlog.
	if err := DockerCommand("start", chContainerName); err != nil {
		t.Fatalf("docker start: %v", err)
	}
	t.Logf("CH restarted; waiting for drain")

	// Re-open store after restart for the count check.
	store2, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs:    []string{CHAddr},
		Database: "statnive",
		Username: "default",
	}, logger)
	if err != nil {
		// CH may take a few seconds to come back up — retry briefly.
		deadline := time.Now().Add(30 * time.Second)

		for time.Now().Before(deadline) {
			time.Sleep(time.Second)

			store2, err = storage.NewClickHouseStore(ctx, storage.Config{
				Addrs: []string{CHAddr}, Database: "statnive", Username: "default",
			}, logger)
			if err == nil {
				break
			}
		}

		if err != nil {
			t.Fatalf("CH did not come back up: %v", err)
		}
	}
	defer func() { _ = store2.Close() }()

	totalSent := pre + gap

	// Phase 7b1b strict gate: the consumer buffered to WAL during the
	// CH outage (Step 2: ack-after-CH-commit means failed inserts
	// leave the WAL intact). After restart, every 2xx-acked event must
	// drain to events_raw within the 0.05% loss SLO. waitForCount
	// shares the helper from crash_recovery_test.go.
	if err := waitForCount(t, ctx, store2, outageSiteID, totalSent, 60*time.Second); err != nil {
		t.Fatalf("ch-outage drain did not meet 0.05%% SLO: %v", err)
	}

	t.Logf("ch-outage summary: sent=%d drained within 0.05%% SLO", totalSent)
}
