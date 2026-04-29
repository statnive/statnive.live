package ingest

import (
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/statnive/statnive.live/internal/metrics"
)

// FillRatioReporter is the subset of WALWriter the back-pressure
// middleware needs. Defined as an interface so tests can inject a
// stub that returns a programmable ratio without touching the disk.
type FillRatioReporter interface {
	FillRatio() float64
}

// BackpressureConfig tunes the WAL-fill back-pressure middleware.
// Defaults match the CLAUDE.md PLAN.md:159 / wal-durability-review
// item #6 contract: 503 at fill_ratio ≥ 0.80 with Retry-After: 5.
type BackpressureConfig struct {
	// Threshold is the WAL fill ratio that triggers 503. Default 0.80.
	Threshold float64
	// CacheTTL caps how often the middleware re-reads FillRatio (which
	// walks the WAL directory, ~10ms even on SSD). Default 1s — well
	// inside the consumer's 500ms batch interval, so the threshold flip
	// surfaces to handlers within ~1.5s of the consumer first noticing.
	CacheTTL time.Duration
	// RetryAfterSeconds is the value sent in the Retry-After header on
	// 503 responses. Default 5.
	RetryAfterSeconds int
	// OnSample is called once per TTL refresh with the sampled fill
	// ratio. Used by main.go to feed the Phase 8 alerts sink — the
	// emitter computes band transitions (0.80 / 0.90 / 0.95) and
	// writes to /var/log/statnive-live/alerts.jsonl. Nil-safe.
	OnSample func(ratio float64)
	// Metrics receives the wal_backpressure counter increment on every
	// 503 the middleware emits. Optional — nil-safe.
	Metrics *metrics.Registry
}

// BackpressureMiddleware returns 503 + Retry-After when the WAL fill
// ratio crosses Threshold. Wired in front of /api/event so a stuck
// consumer (CH down beyond retry budget) eventually pushes back to
// trackers instead of silently filling the WAL until cap-fire drops
// the oldest segments.
//
// FillRatio walks the WAL directory each call, so the result is cached
// for CacheTTL — a 7K EPS load would otherwise burn the directory walk
// per-request. The cache is best-effort: between TTL refreshes a
// crossing-the-threshold consumer drains for one TTL window before
// handlers see the new ratio.
func BackpressureMiddleware(reporter FillRatioReporter, cfg BackpressureConfig) func(http.Handler) http.Handler {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 0.80
	}

	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = time.Second
	}

	if cfg.RetryAfterSeconds <= 0 {
		cfg.RetryAfterSeconds = 5
	}

	retryAfter := strconv.Itoa(cfg.RetryAfterSeconds)

	g := &fillRatioGate{
		reporter:  reporter,
		threshold: cfg.Threshold,
		ttl:       cfg.CacheTTL,
		onSample:  cfg.OnSample,
	}

	reg := cfg.Metrics

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if g.degraded() {
				reg.IncDropped(metrics.ReasonWALBackpressure)
				w.Header().Set("Retry-After", retryAfter)
				http.Error(w, "wal back-pressure", http.StatusServiceUnavailable)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

type fillRatioGate struct {
	reporter  FillRatioReporter
	threshold float64
	ttl       time.Duration
	onSample  func(ratio float64)

	mu          sync.Mutex
	lastChecked time.Time
	cachedAbove atomic.Bool
}

// degraded returns true when the WAL is at/above threshold. Refreshes
// at most once per ttl. The lock is held only across the FillRatio
// call (which itself is bounded — directory walk on a 10 GB cap WAL
// has < 1000 segments, ~10ms).
func (g *fillRatioGate) degraded() bool {
	g.mu.Lock()

	if time.Since(g.lastChecked) < g.ttl {
		above := g.cachedAbove.Load()
		g.mu.Unlock()

		return above
	}

	ratio := g.reporter.FillRatio()
	above := ratio >= g.threshold
	g.cachedAbove.Store(above)
	g.lastChecked = time.Now()
	g.mu.Unlock()

	if g.onSample != nil {
		g.onSample(ratio)
	}

	return above
}
