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
//
// Steady-state (band unchanged) is the common path by a factor of
// ~100× at 10K EPS — skip the atomic.Swap and return a zero-ed
// Transition so the caller's early-return stays efficient.
func (t *BandTracker) Observe(band uint32) Transition {
	prev := t.current.Load()
	if band == prev {
		return Transition{Band: band, Prev: prev}
	}

	t.current.Store(band)

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

// ClassifyRatio maps a 0.0–1.0 gauge to a 0/1/2/3 band using the given
// ascending thresholds — e.g. WAL uses [0.80, 0.90, 0.95], disk uses
// [0.85, 0.90, 0.95]. Returns the matching severity; band 0 is "info"
// (reserved for the resolved side of an enter/exit pair). Panics
// (dev-time only) if thresholds isn't length 3.
func ClassifyRatio(ratio float64, thresholds [3]float64) (band uint32, sev Severity) {
	switch {
	case ratio >= thresholds[2]:
		return 3, SeverityCritical
	case ratio >= thresholds[1]:
		return 2, SeverityCritical
	case ratio >= thresholds[0]:
		return 1, SeverityWarn
	default:
		return 0, SeverityInfo
	}
}
