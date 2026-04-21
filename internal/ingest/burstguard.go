package ingest

import (
	"sync"
	"time"
)

// BurstGuard caps per-visitor pageviews-per-minute. Doc 24 §Sec 5 T2 #15
// — without it, a single misbehaving tracker (or scraper-network bot)
// can inflate is_new and drain ClickHouse capacity. The guard sits in
// the enrichment pipeline AFTER identity (so we have visitor_hash) but
// BEFORE bloom (so a burst doesn't pollute the new-visitor counter).
//
// State is a sharded map[visitor_hash]{count, windowStart}. Eviction is
// lazy on Allow — when the window expires, the next Allow resets the
// entry. No background goroutine; the map grows with active visitors
// over the past 60s window and naturally bounds at peak concurrent
// visitor count (1.5K in the load test, ~50K at SamplePlatform peak).
type BurstGuard struct {
	cap    int
	shards [256]*burstShard
}

type burstShard struct {
	mu      sync.Mutex
	entries map[[16]byte]burstEntry
}

type burstEntry struct {
	count       int
	windowStart time.Time
}

// burstWindow is the rolling window for the per-visitor counter. 60s
// matches the human-perception cap on "how fast can a real user click
// links" — anything above 500/min is almost certainly a bot or a
// runaway tracker.
const burstWindow = time.Minute

// NewBurstGuard constructs a guard with the given per-minute cap.
// cap <= 0 disables the guard (Allow always returns true).
func NewBurstGuard(maxPerMinute int) *BurstGuard {
	g := &BurstGuard{cap: maxPerMinute}

	for i := range g.shards {
		g.shards[i] = &burstShard{entries: make(map[[16]byte]burstEntry)}
	}

	return g
}

// Allow reports whether a visitor's event should pass through the
// pipeline. Returns true when the cap is disabled, when the visitor
// has no recent history, or when their count for the current window
// is below cap. Returns false (and DOES NOT record the rejection in
// the counter) when the cap is exceeded — the caller decides whether
// to count rejections separately for audit.
func (g *BurstGuard) Allow(visitorHash [16]byte, now time.Time) bool {
	if g.cap <= 0 {
		return true
	}

	shard := g.shards[visitorHash[0]]
	shard.mu.Lock()
	defer shard.mu.Unlock()

	e, ok := shard.entries[visitorHash]
	if !ok || now.Sub(e.windowStart) >= burstWindow {
		shard.entries[visitorHash] = burstEntry{count: 1, windowStart: now}

		return true
	}

	if e.count >= g.cap {
		return false
	}

	e.count++
	shard.entries[visitorHash] = e

	return true
}

// ActiveVisitors returns an approximate count of distinct visitors
// seen in the current 60s window. O(N) in the entry count; use
// sparingly. Diagnostic only.
func (g *BurstGuard) ActiveVisitors() int {
	total := 0

	for _, s := range g.shards {
		s.mu.Lock()
		total += len(s.entries)
		s.mu.Unlock()
	}

	return total
}
