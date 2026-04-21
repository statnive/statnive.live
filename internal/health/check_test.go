package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/ingest"
)

// stubWALSyncer satisfies WALSyncerStatsReporter with a fixed value.
// Lets the /healthz handler test verify field shape without spinning up
// a real GroupSyncer + filesystem.
type stubWALSyncer struct {
	stats ingest.WALSyncerStats
}

func (s stubWALSyncer) Stats() ingest.WALSyncerStats { return s.stats }

// On a fresh process — zero successful syncs — /healthz must emit
// wal_fsync_p99_ms as null so a JSON consumer can distinguish "no
// signal yet" from "fsync is sub-millisecond fast".
func TestHealthz_FsyncP99NullWhenNoSamples(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	Handler(Reporter{
		WALSyncer: stubWALSyncer{},
		Start:     time.Now(),
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}

	v, ok := body["wal_fsync_p99_ms"]
	if !ok {
		t.Fatal("wal_fsync_p99_ms field missing from /healthz body")
	}

	if v != nil {
		t.Errorf("wal_fsync_p99_ms = %v; want null when sample count is 0", v)
	}
}

// Once samples are present, /healthz emits the p99 in milliseconds with
// fractional precision (microsecond → ms / 1000).
func TestHealthz_FsyncP99ReportedInMs(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	Handler(Reporter{
		WALSyncer: stubWALSyncer{stats: ingest.WALSyncerStats{
			FsyncP99:         2500 * time.Microsecond, // 2.5 ms
			FsyncSampleCount: 100,
		}},
		Start: time.Now(),
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}

	got, ok := body["wal_fsync_p99_ms"].(float64)
	if !ok {
		t.Fatalf("wal_fsync_p99_ms not a number; got %T (%v)", body["wal_fsync_p99_ms"], body["wal_fsync_p99_ms"])
	}

	if got != 2.5 {
		t.Errorf("wal_fsync_p99_ms = %v; want 2.5", got)
	}
}

// A high p99 must NOT trigger 503 — the fill_ratio gate is the binary
// up/down signal; fsync p99 is for alerting only.
func TestHealthz_HighFsyncP99StaysOK(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	Handler(Reporter{
		WALSyncer: stubWALSyncer{stats: ingest.WALSyncerStats{
			FsyncP99:         5 * time.Second,
			FsyncSampleCount: 100,
		}},
		Start: time.Now(),
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 (fsync p99 is alerting-only, not 503)", rec.Code)
	}
}

// A nil WALSyncer omits the field entirely (operator-friendly default
// when the syncer isn't wired — e.g. in degraded test rigs).
func TestHealthz_FsyncP99OmittedWhenSyncerNil(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	Handler(Reporter{Start: time.Now()}).ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}

	if _, present := body["wal_fsync_p99_ms"]; present {
		t.Errorf("wal_fsync_p99_ms present without WALSyncer; want field omitted")
	}
}
