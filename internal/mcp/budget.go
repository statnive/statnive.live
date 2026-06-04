package mcp

import (
	"sync"
	"time"
)

// BudgetConfig caps per-actor query volume to stop dataset-cloning and
// over-querying through the read API. A zero value for any cap disables that
// cap. The wildcard actor (legacy bearer / stdio --all-sites) gets every cap
// multiplied by WildcardFactor (<1) because it has the widest blast radius.
type BudgetConfig struct {
	CallsPerMin         int
	RowsPerMin          int
	CallsPerSession     int
	RowsPerSession      int
	DistinctSitesPerMin int
	WildcardFactor      float64
}

type actorBudget struct {
	winStart  time.Time
	calls     int
	rows      int
	siteSet   map[uint32]struct{}
	sessCalls int
	sessRows  int
}

// budgetSet holds per-actor token buckets in memory (no Redis — single
// binary, air-gap-safe). Per-minute counters reset on a fixed window;
// session counters live for the process / actor lifetime.
type budgetSet struct {
	mu  sync.Mutex
	cfg BudgetConfig
	now func() time.Time
	m   map[string]*actorBudget
}

func newBudgetSet(cfg BudgetConfig, now func() time.Time) *budgetSet {
	if now == nil {
		now = time.Now
	}

	return &budgetSet{cfg: cfg, now: now, m: make(map[string]*actorBudget)}
}

// scaleCap applies the wildcard factor; 0 (disabled) stays 0; a scaled value
// floors at 1 so a wildcard actor is never accidentally given an unlimited
// (0) cap.
func scaleCap(base int, wildcard bool, factor float64) int {
	if base <= 0 {
		return 0
	}

	if wildcard && factor > 0 {
		scaled := int(float64(base) * factor)
		if scaled < 1 {
			scaled = 1
		}

		return scaled
	}

	return base
}

// get returns the actor's bucket, resetting the per-minute window if it has
// rolled over. Caller holds the lock.
func (b *budgetSet) get(key string) *actorBudget {
	ab := b.m[key]
	if ab == nil {
		ab = &actorBudget{winStart: b.now(), siteSet: make(map[uint32]struct{})}
		b.m[key] = ab
	}

	if b.now().Sub(ab.winStart) >= time.Minute {
		ab.winStart = b.now()
		ab.calls = 0
		ab.rows = 0
		ab.siteSet = make(map[uint32]struct{})
	}

	return ab
}

// reserve checks the per-minute + per-session call/row caps (rows from prior
// calls in the window count) and increments the call counters. Returns false
// + seconds-until-window-reset when a cap is hit. perToolCallCap (>0) is a
// tighter per-tool ceiling (admin tools).
func (b *budgetSet) reserve(key string, wildcard bool, perToolCallCap int) (ok bool, retryAfter int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ab := b.get(key)

	callCap := scaleCap(b.cfg.CallsPerMin, wildcard, b.cfg.WildcardFactor)
	if perToolCallCap > 0 && (callCap == 0 || perToolCallCap < callCap) {
		callCap = perToolCallCap
	}

	rowCap := scaleCap(b.cfg.RowsPerMin, wildcard, b.cfg.WildcardFactor)
	sessCallCap := scaleCap(b.cfg.CallsPerSession, wildcard, b.cfg.WildcardFactor)
	sessRowCap := scaleCap(b.cfg.RowsPerSession, wildcard, b.cfg.WildcardFactor)

	retry := int(time.Minute.Seconds() - b.now().Sub(ab.winStart).Seconds())
	if retry < 1 {
		retry = 1
	}

	switch {
	case callCap > 0 && ab.calls >= callCap:
		return false, retry
	case rowCap > 0 && ab.rows >= rowCap:
		return false, retry
	case sessCallCap > 0 && ab.sessCalls >= sessCallCap:
		return false, retry
	case sessRowCap > 0 && ab.sessRows >= sessRowCap:
		return false, retry
	}

	ab.calls++
	ab.sessCalls++

	return true, 0
}

// charge records rows from a completed call and returns the current window
// row total + the (wildcard-scaled) per-minute row cap, for anomaly banding.
func (b *budgetSet) charge(key string, wildcard bool, rows int) (windowRows, rowCap int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ab := b.get(key)
	ab.rows += rows
	ab.sessRows += rows

	return ab.rows, scaleCap(b.cfg.RowsPerMin, wildcard, b.cfg.WildcardFactor)
}

// noteSite records a distinct site touched this window and returns the
// distinct count + the (wildcard-scaled) threshold, for cross-tenant-sweep
// detection.
func (b *budgetSet) noteSite(key string, wildcard bool, siteID uint32) (distinct, threshold int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ab := b.get(key)
	ab.siteSet[siteID] = struct{}{}

	return len(ab.siteSet), scaleCap(b.cfg.DistinctSitesPerMin, wildcard, b.cfg.WildcardFactor)
}
