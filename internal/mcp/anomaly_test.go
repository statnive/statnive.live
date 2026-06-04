package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/alerts"
)

func newTestSink(t *testing.T) (*alerts.Sink, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "alerts.jsonl")

	sink, err := alerts.New(path, "testhost")
	if err != nil {
		t.Fatalf("alerts.New: %v", err)
	}

	t.Cleanup(func() { _ = sink.Close() })

	return sink, path
}

func TestAnomaly_BulkReadAlertFiresAndDebounces(t *testing.T) {
	t.Parallel()

	sink, path := newTestSink(t)
	d := newAnomalyDetector(sink)

	ctx := context.Background()

	// Cross 100% of the row cap → critical band entry → one emit.
	d.observeRows(ctx, "user:abc", "key1", 100, 100)
	// Same band again → debounced (no second emit).
	d.observeRows(ctx, "user:abc", "key1", 120, 100)

	data, _ := os.ReadFile(path) //nolint:gosec // G304: reads a temp alerts file this test created
	s := string(data)

	// Count the "alert":"<name>" token (appears once per emitted line; the
	// name also shows in "msg", so a bare substring count double-counts).
	if got := strings.Count(s, `"alert":"`+alertBulkRead+`"`); got != 1 {
		t.Fatalf("bulk-read alert count = %d, want 1 (debounced):\n%s", got, s)
	}

	if !strings.Contains(s, "user:abc") {
		t.Errorf("alert missing actor label:\n%s", s)
	}

	// Drop back below 50% → one resolved (exit-band) emit.
	d.observeRows(ctx, "user:abc", "key1", 10, 100)

	data, _ = os.ReadFile(path) //nolint:gosec // G304: reads a temp alerts file this test created
	if !strings.Contains(string(data), `"resolved":true`) {
		t.Errorf("expected a resolved exit-band alert:\n%s", data)
	}
}

func TestAnomaly_CrossTenantSweep(t *testing.T) {
	t.Parallel()

	sink, path := newTestSink(t)
	d := newAnomalyDetector(sink)

	ctx := context.Background()

	// Within threshold → no alert.
	d.observeSweep(ctx, "token:wildcard", "k", 5, 5)
	// Over threshold → the dataset-clone signal fires.
	d.observeSweep(ctx, "token:wildcard", "k", 6, 5)

	data, _ := os.ReadFile(path) //nolint:gosec // G304: reads a temp alerts file this test created
	s := string(data)

	if got := strings.Count(s, `"alert":"`+alertCrossTenantSweep+`"`); got != 1 {
		t.Fatalf("cross-tenant-sweep alert count = %d, want 1 (fire once on band entry):\n%s", got, s)
	}
}

func TestAnomaly_NilSinkIsNoop(t *testing.T) {
	t.Parallel()

	d := newAnomalyDetector(nil)

	// Must not panic with a disabled (nil) sink.
	d.observeRows(context.Background(), "u", "k", 100, 100)
	d.observeSweep(context.Background(), "u", "k", 99, 5)
}

func TestAnomaly_DisabledWhenCapZero(t *testing.T) {
	t.Parallel()

	sink, path := newTestSink(t)
	d := newAnomalyDetector(sink)

	// A zero cap/threshold means "disabled" — no alert regardless of volume.
	d.observeRows(context.Background(), "u", "k", 1_000_000, 0)
	d.observeSweep(context.Background(), "u", "k", 1_000, 0)

	data, _ := os.ReadFile(path) //nolint:gosec // G304: reads a temp alerts file this test created
	if len(strings.TrimSpace(string(data))) != 0 {
		t.Errorf("expected no alerts with zero caps, got:\n%s", data)
	}
}
