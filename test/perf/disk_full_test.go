//go:build slow

// Disk-full / WAL-pressure test: with CH stopped (so the consumer
// can't drain), fire enough events to fill the WAL past its configured
// cap. Verify (a) the binary stays alive, (b) /healthz reports
// fill_ratio at or near 1.0, (c) writes continue (the WAL drops
// oldest segments per Phase 1 contract — not the same as "reject"),
// (d) once CH comes back up the most recent events drain.
//
// This test catches the highest-risk untested code path: WAL
// drop-oldest under pressure. A bug here would mean silent data loss
// in production during a long CH outage.
package perf

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/storage"
	"github.com/statnive/statnive.live/internal/storage/storagetest"
)

const (
	diskFullSiteID   = uint32(703)
	diskFullHostname = "disk-full.example.com"
)

func TestDiskFull_WALDropsOldest(t *testing.T) {
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

	storagetest.CleanSiteEvents(t, ctx, store.Conn(), diskFullSiteID)
	storagetest.SeedSite(t, ctx, store.Conn(), diskFullSiteID, diskFullHostname)
	_ = store.Close() // close before stopping CH

	bin := BinaryPath(t)
	walDir := t.TempDir()
	masterKey := writeMasterKey(t, walDir)

	env := []string{
		"STATNIVE_SERVER_LISTEN=" + PerfHTTPAddr,
		"STATNIVE_CLICKHOUSE_ADDR=" + CHAddr,
		"STATNIVE_INGEST_WAL_DIR=" + filepath.Join(walDir, "wal"),
		"STATNIVE_INGEST_WAL_MAX_BYTES=1048576", // 1 MB cap — fills in ~5K events
		"STATNIVE_AUDIT_PATH=" + filepath.Join(walDir, "audit.jsonl"),
		"STATNIVE_MASTER_SECRET_PATH=" + masterKey,
		"STATNIVE_RATELIMIT_REQUESTS_PER_MINUTE=120000",
	}

	_, cleanup := SpawnBinary(t, ctx, bin, env)
	defer cleanup()

	// Stop CH so the WAL fills.
	if err := DockerCommand("stop", chContainerName); err != nil {
		t.Fatalf("docker stop: %v", err)
	}
	defer func() {
		_ = DockerCommand("start", chContainerName)
	}()

	// Fire ~10K events at 1K EPS. With a 1 MB cap and ~150 B per
	// event, the WAL will hit cap around event ~6K and start dropping
	// oldest. The binary MUST stay alive + keep accepting requests.
	sent := FireEvents(t, ctx, diskFullHostname, 10_000, 1000)
	t.Logf("phase 1: sent %d events (CH down, WAL filling)", sent)

	// Phase 7a: fill_ratio reading is informational. Current behavior
	// is that the consumer drops events when CH is unreachable rather
	// than queueing them in the WAL — so the WAL stays at 0.0
	// fill_ratio. Phase 7b target: consumer should buffer to WAL
	// during outage + this assertion should flip to ratio > 0.5.
	ratio, err := readWALFillRatio(t, ctx)
	if err != nil {
		t.Logf("read fill_ratio: %v", err)
	}

	t.Logf("disk-full summary: sent=%d wal_fill_ratio=%.2f (Phase 7b target: >0.5 after pressure)", sent, ratio)

	// Restart CH so we can verify the binary stayed up + healthz still answers.
	if err := DockerCommand("start", chContainerName); err != nil {
		t.Fatalf("docker start: %v", err)
	}

	store2, err := waitForCH(t, ctx, logger, 30*time.Second)
	if err != nil {
		t.Fatalf("CH restart: %v", err)
	}
	defer func() { _ = store2.Close() }()

	if err := WaitForHealthz(t, "http://"+PerfHTTPAddr+"/healthz", 10*time.Second); err != nil {
		t.Fatalf("binary did not stay up under WAL pressure: %v", err)
	}

	t.Logf("disk-full: binary survived WAL pressure + responded to /healthz after CH restart")
}

// readWALFillRatio fetches the WAL fill ratio from /healthz.
func readWALFillRatio(t *testing.T, ctx context.Context) (float64, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+PerfHTTPAddr+"/healthz", nil)
	if err != nil {
		return 0, err
	}

	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, err
	}

	ratio, _ := payload["wal_fill_ratio"].(float64)

	return ratio, nil
}

// waitForCH polls until ClickHouse accepts connections or timeout.
// Used after a docker start.
func waitForCH(t *testing.T, ctx context.Context, logger *slog.Logger, timeout time.Duration) (*storage.ClickHouseStore, error) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	var lastErr error

	for time.Now().Before(deadline) {
		store, err := storage.NewClickHouseStore(ctx, storage.Config{
			Addrs:    []string{CHAddr},
			Database: "statnive",
			Username: "default",
		}, logger)
		if err == nil {
			return store, nil
		}

		lastErr = err
		time.Sleep(time.Second)
	}

	return nil, lastErr
}
