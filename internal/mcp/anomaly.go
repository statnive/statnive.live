package mcp

import (
	"context"
	"log/slog"
	"sync"

	"github.com/statnive/statnive.live/internal/alerts"
)

// Alert names for the MCP read surface. Deterministic, threshold-based — no
// model (inside the no-LLM invariant), no outbound (alerts.Sink is a local
// JSONL file).
const (
	alertBulkRead         = "mcp_bulk_read_anomaly"
	alertCrossTenantSweep = "mcp_cross_tenant_sweep"
)

// anomalyDetector emits debounced ops alerts when a single actor's read
// volume or cross-tenant breadth crosses bands — the "someone is cloning the
// dataset" signal. Per-actor BandTrackers keep one emit per real transition.
// A nil sink (alerts disabled) makes every method a no-op.
type anomalyDetector struct {
	sink *alerts.Sink

	mu    sync.Mutex
	rows  map[string]*alerts.BandTracker
	sweep map[string]*alerts.BandTracker
}

func newAnomalyDetector(sink *alerts.Sink) *anomalyDetector {
	return &anomalyDetector{
		sink:  sink,
		rows:  make(map[string]*alerts.BandTracker),
		sweep: make(map[string]*alerts.BandTracker),
	}
}

func (a *anomalyDetector) tracker(m map[string]*alerts.BandTracker, key string) *alerts.BandTracker {
	a.mu.Lock()
	defer a.mu.Unlock()

	t := m[key]
	if t == nil {
		t = &alerts.BandTracker{}
		m[key] = t
	}

	return t
}

// observeRows bands the window row total against the cap (≥50% warn, ≥100%
// critical) and emits on band entry; emits a resolved alert on return to 0.
func (a *anomalyDetector) observeRows(ctx context.Context, label, key string, windowRows, rowCap int) {
	if a == nil || a.sink == nil || rowCap <= 0 {
		return
	}

	band, sev := rowBand(windowRows, rowCap)
	tr := a.tracker(a.rows, key).Observe(band)

	switch {
	case tr.Entered:
		a.sink.Emit(ctx, alertBulkRead, sev, false,
			slog.String("actor", label),
			slog.Int("window_rows", windowRows),
			slog.Int("row_cap", rowCap))
	case tr.Exited && tr.Band == 0:
		a.sink.Emit(ctx, alertBulkRead, alerts.SeverityInfo, true, slog.String("actor", label))
	}
}

// observeSweep emits when an actor touches more distinct sites this window
// than the threshold — the dataset-clone / cross-tenant signal.
func (a *anomalyDetector) observeSweep(ctx context.Context, label, key string, distinct, threshold int) {
	if a == nil || a.sink == nil || threshold <= 0 {
		return
	}

	var band uint32
	if distinct > threshold {
		band = 1
	}

	tr := a.tracker(a.sweep, key).Observe(band)
	if tr.Entered {
		a.sink.Emit(ctx, alertCrossTenantSweep, alerts.SeverityWarn, false,
			slog.String("actor", label),
			slog.Int("distinct_sites", distinct),
			slog.Int("threshold", threshold))
	}
}

func rowBand(n, limit int) (uint32, alerts.Severity) {
	switch {
	case n >= limit:
		return 2, alerts.SeverityCritical
	case n*2 >= limit: // ≥50%
		return 1, alerts.SeverityWarn
	default:
		return 0, alerts.SeverityInfo
	}
}
