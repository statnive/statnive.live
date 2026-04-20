package ingest_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/ingest"
)

// stubReporter returns a programmable fill ratio and counts how often
// FillRatio was consulted — lets tests verify the TTL cache honors its
// bound instead of burning a disk walk per request.
type stubReporter struct {
	ratio atomic.Uint64 // *float64 in atomic form: bits of the float
	calls atomic.Int32
}

func (r *stubReporter) set(v float64) {
	//nolint:gosec // test scratch: v is always in [0, 2] so the cast is safe.
	bits := uint64(int64(v * 1e9))
	r.ratio.Store(bits)
}

func (r *stubReporter) FillRatio() float64 {
	r.calls.Add(1)
	bits := r.ratio.Load()

	//nolint:gosec // test scratch helper; ratio always in [0, 2]
	return float64(int64(bits)) / 1e9
}

func post(handler http.Handler) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/event", nil)
	handler.ServeHTTP(rr, req)

	return rr
}

// Below threshold: every request passes through to the inner handler.
func TestBackpressure_BelowThresholdPassesThrough(t *testing.T) {
	t.Parallel()

	r := &stubReporter{}
	r.set(0.5)

	downstream := int32(0)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&downstream, 1)
		w.WriteHeader(http.StatusAccepted)
	})
	h := ingest.BackpressureMiddleware(r, ingest.BackpressureConfig{})(inner)

	for range 5 {
		rr := post(h)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status = %d; want 202", rr.Code)
		}
	}

	if got := atomic.LoadInt32(&downstream); got != 5 {
		t.Errorf("downstream calls = %d; want 5", got)
	}
}

// At/above threshold: every request short-circuits with 503 + Retry-After.
func TestBackpressure_AtOrAboveThresholdReturns503(t *testing.T) {
	t.Parallel()

	r := &stubReporter{}
	r.set(0.85)

	downstream := int32(0)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&downstream, 1)
		w.WriteHeader(http.StatusAccepted)
	})
	h := ingest.BackpressureMiddleware(r, ingest.BackpressureConfig{})(inner)

	rr := post(h)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; want 503", rr.Code)
	}

	if got := rr.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q; want 5", got)
	}

	if got := atomic.LoadInt32(&downstream); got != 0 {
		t.Errorf("downstream should not fire under back-pressure; got %d calls", got)
	}
}

// Exactly AT threshold counts as degraded (≥ 0.80 boundary).
func TestBackpressure_ExactlyAtThresholdIs503(t *testing.T) {
	t.Parallel()

	r := &stubReporter{}
	r.set(0.80)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	h := ingest.BackpressureMiddleware(r, ingest.BackpressureConfig{})(inner)

	rr := post(h)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("ratio == Threshold must be 503; got %d", rr.Code)
	}
}

// TTL cache: FillRatio is consulted once per TTL window, not per request.
func TestBackpressure_TTLCache(t *testing.T) {
	t.Parallel()

	r := &stubReporter{}
	r.set(0.5)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	h := ingest.BackpressureMiddleware(r, ingest.BackpressureConfig{
		CacheTTL: 100 * time.Millisecond,
	})(inner)

	// 100 requests within the TTL window.
	for range 100 {
		post(h)
	}

	if got := r.calls.Load(); got != 1 {
		t.Errorf("FillRatio consulted %d times within TTL; want 1", got)
	}

	// After the TTL expires, a new request refreshes.
	time.Sleep(120 * time.Millisecond)
	post(h)

	if got := r.calls.Load(); got != 2 {
		t.Errorf("FillRatio consulted %d times after TTL; want 2", got)
	}
}

// Recovery: when the ratio drops below threshold, traffic resumes
// (after the TTL refresh).
func TestBackpressure_RecoveryAfterRefresh(t *testing.T) {
	t.Parallel()

	r := &stubReporter{}
	r.set(0.9)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	h := ingest.BackpressureMiddleware(r, ingest.BackpressureConfig{
		CacheTTL: 50 * time.Millisecond,
	})(inner)

	// Initially degraded.
	if rr := post(h); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("first request should be 503; got %d", rr.Code)
	}

	// Consumer drains below threshold.
	r.set(0.3)

	// Same TTL window — stale cache still says degraded.
	if rr := post(h); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("within TTL, cached degraded state should persist")
	}

	// After TTL, new ratio surfaces.
	time.Sleep(70 * time.Millisecond)

	if rr := post(h); rr.Code != http.StatusAccepted {
		t.Errorf("after TTL, recovered ratio should let traffic through; got %d", rr.Code)
	}
}
