package goals

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"
)

// Matcher is the ingest-hot-path contract. The pipeline never imports
// *Snapshot directly — it takes a Matcher so tests inject a NopMatcher
// without a CH connection. Production always gets *Snapshot.
type Matcher interface {
	Match(siteID uint32, eventName string) (goalID uuid.UUID, valueRials uint64, ok bool)
}

// Snapshot is the in-memory goals cache feeding the ingest hook. Read
// path is an atomic.Pointer load + map lookup + bounded linear scan;
// zero allocation, zero mutex, no CH round-trip. Reload rebuilds the
// map off the hot path + atomic-swaps.
//
// Hot-swap idiom mirrors internal/enrich/geoip.go (see
// geoip-pipeline-review skill). No fsnotify; SIGHUP calls Reload.
type Snapshot struct {
	store Store

	// ptr holds map[siteID][]Goal — per-site linear scan.
	// nil until first Reload completes; Match on nil returns no-match.
	ptr atomic.Pointer[map[uint32][]Goal]
}

// NewSnapshot builds a Snapshot + performs the initial load. Returns
// an error if the first CH read fails — caller (main.go) MUST fail
// boot rather than ship with an empty goal set; v1.1 may relax this
// once we have a disk-cached fallback.
func NewSnapshot(ctx context.Context, store Store) (*Snapshot, error) {
	s := &Snapshot{store: store}

	if err := s.Reload(ctx); err != nil {
		return nil, fmt.Errorf("initial goals load: %w", err)
	}

	return s, nil
}

// Match is the hot-path lookup. Returns (goalID, valueRials, true) on
// a hit; zero values + false on miss. Pure string equality — no
// allocations, no mutex, no CH call.
func (s *Snapshot) Match(siteID uint32, eventName string) (uuid.UUID, uint64, bool) {
	if s == nil {
		return uuid.Nil, 0, false
	}

	m := s.ptr.Load()
	if m == nil {
		return uuid.Nil, 0, false
	}

	goals := (*m)[siteID]
	for i := range goals {
		g := &goals[i]
		if g.MatchType == MatchTypeEventNameEquals && g.Pattern == eventName {
			return g.GoalID, g.Value, true
		}
	}

	return uuid.Nil, 0, false
}

// Reload pulls the full enabled-goal set from the Store + atomic-swaps
// the in-memory map. Fail-closed: on error, the previous snapshot is
// retained. Safe to call concurrently; last writer wins.
func (s *Snapshot) Reload(ctx context.Context) error {
	if s.store == nil {
		return errors.New("snapshot: nil store")
	}

	active, err := s.store.ListActive(ctx)
	if err != nil {
		return fmt.Errorf("list active goals: %w", err)
	}

	bySite := make(map[uint32][]Goal, 8)

	for _, g := range active {
		if g == nil {
			continue
		}

		bySite[g.SiteID] = append(bySite[g.SiteID], *g)
	}

	s.ptr.Store(&bySite)

	return nil
}

// Size returns the total goal count in the current snapshot. Used by
// /healthz + admin-UI "N active goals" indicator.
func (s *Snapshot) Size() int {
	if s == nil {
		return 0
	}

	m := s.ptr.Load()
	if m == nil {
		return 0
	}

	total := 0

	for _, goals := range *m {
		total += len(goals)
	}

	return total
}

// NopMatcher is the no-op Matcher for tests + code paths that don't
// care about goals. Never matches; used as a nil-safe default in
// enrich.Deps test fixtures so the pipeline's non-nil-dep guard is
// satisfied without spinning up a real CH-backed Snapshot.
type NopMatcher struct{}

// Match always returns no-match.
func (NopMatcher) Match(uint32, string) (uuid.UUID, uint64, bool) {
	return uuid.Nil, 0, false
}

var (
	_ Matcher = (*Snapshot)(nil)
	_ Matcher = NopMatcher{}
)
