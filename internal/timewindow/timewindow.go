// Package timewindow holds the shared date-window primitives used by both
// the dashboard HTTP layer and the MCP server. Keeping them here lets the
// MCP package parse ranges without importing internal/dashboard (the HTTP
// layer), and guarantees the two surfaces interpret "7d" / "2026-04-01"
// identically — a hard requirement for the MCP↔dashboard CH-oracle parity
// tests.
//
// Two conventions, both producing a UTC half-open interval [from, to):
//
//   - Explicit YYYY-MM-DD dates are interpreted as midnight in the site's
//     IANA zone, then converted to UTC, so a "day" respects the site's
//     calendar (e.g. Asia/Tehran's day starts 20:30 UTC the prior day).
//   - Hour shorthands (Nh) align to the UTC hour, because the hourly
//     rollup is keyed by DateTime('UTC') — local hour boundaries would
//     miss the rollup grain on half-hour zones.
package timewindow

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DateLayout is the only explicit-date format accepted (YYYY-MM-DD).
const DateLayout = "2006-01-02"

// DefaultRangeDays is the look-back window when no range is supplied.
const DefaultRangeDays = 7

// maxRangeDays bounds shorthand ranges with a friendly error before the
// stricter storage.Filter.Validate (MaxRange = 365 days) is reached, so
// an LLM gets "max range is 365 days" rather than a generic validation
// failure. Kept in sync with storage.MaxRange (365d) by intent, not import
// (timewindow must not depend on storage).
const maxRangeDays = 365

// ErrBadRange is the umbrella error for any unparseable / out-of-bounds
// range input. Callers (dashboard → HTTP 400, MCP → JSON-RPC -32602) wrap
// it into their own boundary error.
var ErrBadRange = errors.New("timewindow: invalid range")

// ParseLocalDate parses YYYY-MM-DD as midnight in loc. Empty input returns
// the zero time so callers can distinguish "missing" from "invalid".
func ParseLocalDate(raw string, loc *time.Location) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}

	t, err := time.ParseInLocation(DateLayout, raw, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q is not YYYY-MM-DD", ErrBadRange, raw)
	}

	return t, nil
}

// ParseDateRange returns the [from, to) half-open interval in UTC for the
// dashboard's from/to query params. When toRaw is empty, to defaults to
// tomorrow's midnight in loc (so the window includes the whole current
// local day). When fromRaw is empty, from defaults to the upper bound minus
// DefaultRangeDays.
// now is injected for deterministic tests; pass time.Now() in production.
func ParseDateRange(fromRaw, toRaw string, loc *time.Location, now time.Time) (from, to time.Time, err error) {
	nowLocal := now.In(loc)

	to, err = ParseLocalDate(toRaw, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if to.IsZero() {
		to = startOfLocalDay(nowLocal, loc).AddDate(0, 0, 1)
	}

	from, err = ParseLocalDate(fromRaw, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if from.IsZero() {
		from = to.AddDate(0, 0, -DefaultRangeDays)
	}

	return from.UTC(), to.UTC(), nil
}

// ParseRange resolves a single range token into a UTC half-open interval:
//
//   - ""               → defaults to "7d"
//   - "Nd" (1d, 30d…)  → day-aligned to local midnight; to = tomorrow
//     local-midnight, from = to - N days (includes today + N-1 prior days)
//   - "Nh" (1h, 24h…)  → UTC-hour-aligned; to = end of the current UTC
//     hour, from = to - N hours ("1h" is exactly the current rollup hour)
//   - "A..B"           → explicit YYYY-MM-DD dates in loc, end-exclusive
//
// Bounds beyond ~365 days return ErrBadRange. The realtime tool ignores
// the range entirely (it always reads the current hour) — callers gate that.
func ParseRange(s string, loc *time.Location, now time.Time) (from, to time.Time, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "7d"
	}

	if lhs, rhs, ok := strings.Cut(s, ".."); ok {
		return parseExplicitRange(lhs, rhs, loc)
	}

	return parseShorthand(s, loc, now)
}

func parseExplicitRange(lhs, rhs string, loc *time.Location) (time.Time, time.Time, error) {
	from, err := ParseLocalDate(strings.TrimSpace(lhs), loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	to, err := ParseLocalDate(strings.TrimSpace(rhs), loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if from.IsZero() || to.IsZero() {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: explicit range needs both A..B dates", ErrBadRange)
	}

	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: start must be before end (end is exclusive)", ErrBadRange)
	}

	if to.Sub(from) > maxRangeDays*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: max range is %d days", ErrBadRange, maxRangeDays)
	}

	return from.UTC(), to.UTC(), nil
}

func parseShorthand(s string, loc *time.Location, now time.Time) (time.Time, time.Time, error) {
	if len(s) < 2 {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: %q (want Nd|Nh or A..B)", ErrBadRange, s)
	}

	unit := s[len(s)-1]
	n, convErr := strconv.Atoi(s[:len(s)-1])

	if convErr != nil || n <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: %q (want a positive Nd|Nh or A..B)", ErrBadRange, s)
	}

	switch unit {
	case 'd':
		if n > maxRangeDays {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: max range is %d days", ErrBadRange, maxRangeDays)
		}

		to := startOfLocalDay(now.In(loc), loc).AddDate(0, 0, 1)
		from := to.AddDate(0, 0, -n)

		return from.UTC(), to.UTC(), nil
	case 'h':
		if n > maxRangeDays*24 {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: max range is %d days", ErrBadRange, maxRangeDays)
		}

		to := now.UTC().Truncate(time.Hour).Add(time.Hour)
		from := to.Add(-time.Duration(n) * time.Hour)

		return from, to, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("%w: unit %q (want d or h)", ErrBadRange, string(unit))
	}
}

// startOfLocalDay returns midnight of t's calendar day in loc.
func startOfLocalDay(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}
