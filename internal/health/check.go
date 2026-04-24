// Package health backs /healthz. Returns 200 + JSON when the binary is
// serving + ClickHouse is reachable + the WAL is below its 80% fill
// threshold (PLAN.md:159 verification 9/11). Anything else returns 503.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/storage"
)

// walFillCritical is the WAL-fill threshold that flips /healthz to 503.
// At 80% the operator has roughly 20% headroom to drain or extend disk
// before the size-cap enforcer starts dropping segments. Source: the WAL
// 10 GB cap + 80% alert pair we ship by default; revisit if disk-full
// triage in production shows this is too eager or too lax.
const walFillCritical = 0.80

// WALSyncerStatsReporter is the subset of *ingest.GroupSyncer that
// /healthz needs. Defined as an interface so tests can wire a stub
// without spinning up a real syncer + filesystem.
type WALSyncerStatsReporter interface {
	Stats() ingest.WALSyncerStats
}

// Reporter is the runtime contract /healthz reads. The fields are pointers
// so tests can construct one with nil for whichever signal isn't relevant.
type Reporter struct {
	Store     *storage.ClickHouseStore
	WAL       *ingest.WALWriter
	WALSyncer WALSyncerStatsReporter
	Start     time.Time
}

// Handler returns the http.Handler for GET /healthz.
func Handler(r Reporter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := map[string]any{
			"status":     "ok",
			"uptime_sec": int64(time.Since(r.Start).Seconds()),
		}
		status := http.StatusOK

		if r.Store != nil {
			ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)

			pingErr := r.Store.Ping(ctx)

			cancel()

			if pingErr != nil {
				body["clickhouse"] = "down: " + pingErr.Error()
				body["status"] = "degraded"
				status = http.StatusServiceUnavailable
			} else {
				body["clickhouse"] = "up"

				body["rows_inserted"] = r.Store.RowsInserted()
				if last := r.Store.LastInsert(); !last.IsZero() {
					body["last_insert"] = last.UTC().Format(time.RFC3339)
				}
			}
		}

		if r.WAL != nil {
			ratio := r.WAL.FillRatio()
			body["wal_fill_ratio"] = ratio

			if ratio >= walFillCritical {
				body["status"] = "degraded"
				status = http.StatusServiceUnavailable
			}
		}

		// WAL fsync p99 — emit nil when no samples yet so a JSON consumer
		// can distinguish "fresh start, no signal" from "everything fast".
		// Not a 503 trigger: this is an alerting threshold, not a binary
		// up/down signal (the fill_ratio gate already covers down-shift).
		if r.WALSyncer != nil {
			stats := r.WALSyncer.Stats()
			if stats.FsyncSampleCount > 0 {
				body["wal_fsync_p99_ms"] = float64(stats.FsyncP99.Microseconds()) / 1000.0
			} else {
				body["wal_fsync_p99_ms"] = nil
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
}
