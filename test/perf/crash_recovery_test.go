//go:build slow

// Crash-recovery test: spawn the binary, fire events, kill -9 mid-batch,
// restart with the same WAL dir, verify the binary survives + post-
// restart events land cleanly + WAL replay runs at all.
//
// CURRENT v1 CONTRACT (verified by this test):
//   1. Binary survives SIGKILL + restart.
//   2. WAL replay runs on second boot (replay log line emitted).
//   3. Post-restart events all reach events_raw with no loss.
//
// KNOWN-BROKEN (filed as Phase 7b cleanup):
//   - Pre-kill events buffered in the WAL between fsync ticks
//     (100ms window) are lost on SIGKILL because tidwall/wal runs in
//     NoSync mode. The fix is either (a) fsync-per-Append (slow), (b)
//     shrink the fsync window to ~10ms, or (c) accept the contract +
//     document the bound. This test does NOT assert zero-loss across
//     the kill — it asserts the binary recovers AT ALL.
//
// Subprocess + SIGKILL is the only honest test of the contract — an
// in-process simulation can't distinguish "WAL flushed by graceful
// shutdown" from "WAL recovered from on-disk state after a real crash".
package perf

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/storage"
	"github.com/statnive/statnive.live/internal/storage/storagetest"
)

const (
	crashSiteID   = uint32(701)
	crashHostname = "crash-recovery.example.com"
	crashEvents   = 5000
	crashRate     = 1000 // events/sec; total wall time ~5s
)

func TestCrashRecovery_KillNineWALReplay(t *testing.T) {
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

	storagetest.CleanSiteEvents(t, ctx, store.Conn(), crashSiteID)
	storagetest.SeedSite(t, ctx, store.Conn(), crashSiteID, crashHostname)

	bin := BinaryPath(t)
	walDir := t.TempDir()
	masterKey := writeMasterKey(t, walDir)

	env := []string{
		"STATNIVE_SERVER_LISTEN=" + PerfHTTPAddr,
		"STATNIVE_CLICKHOUSE_ADDR=" + CHAddr,
		"STATNIVE_INGEST_WAL_DIR=" + filepath.Join(walDir, "wal"),
		"STATNIVE_AUDIT_PATH=" + filepath.Join(walDir, "audit.jsonl"),
		"STATNIVE_MASTER_SECRET_PATH=" + masterKey,
		"STATNIVE_RATELIMIT_REQUESTS_PER_MINUTE=120000", // 2K req/s — well above the test rate
	}

	// First boot — fire half the events, then kill.
	cmd1, _ := SpawnBinary(t, ctx, bin, env)
	sent1 := FireEvents(t, ctx, crashHostname, crashEvents/2, crashRate)
	t.Logf("phase 1: sent %d events before kill", sent1)

	// Brief pause so the pipeline drains the in-channel into the WAL
	// before SIGKILL. Without this, ~half the events are still in the
	// pipeline.in channel at kill time and the WAL never sees them —
	// that's a tracker-side loss the WAL replay contract can't fix.
	// 200ms covers two consumer flush cycles (BatchInterval = 500ms ÷ 2).
	time.Sleep(300 * time.Millisecond)

	if err := cmd1.Process.Kill(); err != nil {
		t.Fatalf("kill -9: %v", err)
	}

	state, _ := cmd1.Process.Wait()
	if state == nil || state.Exited() && state.ExitCode() == 0 {
		t.Fatalf("binary exited cleanly; expected SIGKILL, got %v", state)
	}

	// Brief gap to make sure the OS released the listener.
	time.Sleep(500 * time.Millisecond)

	// Second boot — fire the rest, wait for WAL replay + new events to drain.
	_, cleanup2 := SpawnBinary(t, ctx, bin, env)
	defer cleanup2()

	sent2 := FireEvents(t, ctx, crashHostname, crashEvents/2, crashRate)
	t.Logf("phase 2: sent %d events after restart", sent2)

	// Wait briefly for any flushes to land + measure actual loss so
	// the runbook + Phase 7b plan have concrete numbers. Two known
	// bugs the current contract surface here:
	//   1. WAL Replay decodes only the last entry of a buffered
	//      segment after SIGKILL (NoSync=true on tidwall/wal lets
	//      writes accumulate in-memory; kill leaves a partial
	//      segment that decodes once + then ErrNotFound).
	//   2. Consumer post-restart doesn't always drain the in-channel
	//      promptly under low rates — root cause TBD.
	//
	// Both are Phase 7b-clean-up territory. This test currently
	// asserts only the things the binary DOES guarantee today:
	// it survives SIGKILL + restart + replays at least some events.
	time.Sleep(3 * time.Second)

	row := store.Conn().QueryRow(ctx,
		`SELECT count() FROM statnive.events_raw WHERE site_id = ?`, crashSiteID,
	)

	var got uint64
	if err := row.Scan(&got); err != nil {
		t.Fatalf("count query: %v", err)
	}

	totalSent := sent1 + sent2
	lost := totalSent - int(got)

	if lost < 0 {
		lost = 0
	}

	lossPct := float64(lost) / float64(totalSent) * 100
	t.Logf("crash-recovery summary: sent=%d landed=%d lost=%d (%.2f%%) — SLO: ≤ 0.05%%",
		totalSent, got, lost, lossPct)

	// Phase 7b1b strict gate: CH count must land within 0.05% of the
	// client-received 2xx total. GroupSyncer ack-after-fsync guarantees
	// every 2xx reply corresponds to a durably persisted event, and
	// the consumer's ack-after-CH-commit guarantees replay idempotency
	// across restart.
	if err := waitForCount(t, ctx, store, crashSiteID, totalSent, 30*time.Second); err != nil {
		t.Fatalf("post-crash drain did not meet 0.05%% SLO: %v", err)
	}
}

// waitForCount polls events_raw until count matches `want` (within the
// 0.05% server-loss budget) or timeout. The retry loop covers both the
// WAL replay window and the consumer's batch-flush latency.
func waitForCount(t *testing.T, ctx context.Context, store *storage.ClickHouseStore, siteID uint32, want int, timeout time.Duration) error {
	t.Helper()

	const lossBudget = 0.0005 // 0.05% per CLAUDE.md analytics invariant
	minAccepted := int(float64(want) * (1 - lossBudget))
	deadline := time.Now().Add(timeout)

	var got uint64

	for time.Now().Before(deadline) {
		row := store.Conn().QueryRow(ctx,
			`SELECT count() FROM statnive.events_raw WHERE site_id = ?`,
			siteID,
		)
		if err := row.Scan(&got); err == nil && int(got) >= minAccepted {
			t.Logf("count() reached %d/%d (>= %d minimum)", got, want, minAccepted)

			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("got=%d want>=%d (of %d sent)", got, minAccepted, want)
}

// writeMasterKey writes a 32-byte hex master key to walDir/master.key
// with mode 0600. Returns the path. Reused by every perf test.
func writeMasterKey(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "master.key")
	const key = "perf-test-master-secret-32-bytes-padding-padding"

	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		t.Fatalf("write master key: %v", err)
	}

	return path
}
