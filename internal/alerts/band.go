package alerts

import "sync/atomic"

// BandTracker debounces enter/exit-band transitions for a single alert
// source. Callers sample their gauge continuously (WAL fill ratio, CH
// ping status, disk fill, …) and call Observe on every sample; Observe
// returns a transition that tells the caller whether to Emit.
//
// Why band-tracking matters: an emitter that fires "wal_high_fill_ratio"
// on every backpressure hit would flood the sink + spam the Notice UI.
// The tracker holds the currently-active band and only reports when it
// changes, so one emit lands per real state change.
//
// Safe for concurrent use. The internal state is an atomic uint32 so
// Observe doesn't take a lock on the hot path (every 429 response, every
// CH ping, every health poll).
type BandTracker struct {
	current atomic.Uint32 // zero value = none; higher = more severe
}

// Transition describes the result of an Observe call.
type Transition struct {
	Entered bool   // true when the tracker moved UP to a worse band
	Exited  bool   // true when the tracker moved DOWN (recovery)
	Band    uint32 // band the tracker is NOW in (0 = clear)
	Prev    uint32 // band the tracker WAS in before this call
}

// Observe records a new sample and returns whether the band changed.
// `band` is the caller's encoding of severity: 0 = clear, 1+ = worse.
// For the three bands PLAN.md prescribes on WAL / disk
// (0.80 / 0.90 / 0.95), pass 1 / 2 / 3 respectively.
func (t *BandTracker) Observe(band uint32) Transition {
	prev := t.current.Swap(band)

	return Transition{
		Entered: band > prev,
		Exited:  band < prev,
		Band:    band,
		Prev:    prev,
	}
}

// Current returns the most recently observed band without modifying
// state. Useful for /healthz introspection.
func (t *BandTracker) Current() uint32 {
	return t.current.Load()
}
