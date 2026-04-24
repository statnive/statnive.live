package cache

import "time"

// TTL tiers for dashboard query cache. Doc 24 §Sec 4 pattern 9: Pirsch
// has no query cache; our LRU tier plan is a strict improvement and
// keeps ClickHouse load bounded at SamplePlatform's 10–20M DAU.
const (
	// TTLRealtime — current hour. The hourly_visitors rollup updates
	// continuously as events ingest; 10s gives the dashboard a
	// near-real-time feel without thundering ClickHouse.
	TTLRealtime = 10 * time.Second

	// TTLToday — date range that includes today. The data is still
	// being written; refresh once a minute keeps panels fresh.
	TTLToday = 60 * time.Second

	// TTLYesterday — date range whose latest day is yesterday. New
	// events for yesterday will trickle in via late-arriving WAL
	// replays, but the rate is negligible after midnight + 5min IRST.
	TTLYesterday = 1 * time.Hour

	// TTLHistorical — every datapoint is older than yesterday. Rollup
	// data is effectively immutable; cache for ~1 year so the LRU
	// evicts on capacity, not age. (TTL is required by the underlying
	// LRU library; "forever" is approximated with a year.)
	TTLHistorical = 24 * time.Hour * 365
)

// ResolveTTL returns the appropriate TTL for a query whose latest data
// point is `to`. Computed against `now` so tests can inject a fake
// clock; production callers pass time.Now().UTC().
//
// Buckets, from most-recent to least:
//
//	to >= now-truncated-to-hour     → TTLRealtime
//	to >= now-truncated-to-day      → TTLToday
//	to >= (now - 1d)-truncated-day  → TTLYesterday
//	else                            → TTLHistorical
//
// `to` is the half-open upper bound, so a "today" query passes
// `to = tomorrow-midnight-UTC` and lands in the TTLToday bucket.
func ResolveTTL(now, to time.Time) time.Duration {
	now = now.UTC()
	to = to.UTC()

	currentHour := now.Truncate(time.Hour)
	today := now.Truncate(24 * time.Hour)
	yesterday := today.Add(-24 * time.Hour)

	switch {
	case !to.Before(currentHour):
		return TTLRealtime
	case !to.Before(today):
		return TTLToday
	case !to.Before(yesterday):
		return TTLYesterday
	default:
		return TTLHistorical
	}
}
