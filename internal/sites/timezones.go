package sites

import (
	"fmt"
	"time"
)

// TimezoneOption is the per-row payload of GET /api/admin/timezones.
// The SPA's Add/Edit Site dropdowns render `Label (Offset)` per option;
// the value sent back in PATCH /api/admin/sites/{id} is `IANA`. The
// Offset is computed at request time so DST shifts stay accurate
// without a daily refresh — at the cost of a per-request time.LoadLocation
// call. The list is short (~50 zones) and admin endpoints are
// low-traffic, so the overhead is negligible.
type TimezoneOption struct {
	IANA   string `json:"iana"`   // e.g. "Europe/Berlin"
	Label  string `json:"label"`  // e.g. "Berlin"
	Offset string `json:"offset"` // e.g. "UTC+1" — computed per-request
}

// DefaultTimezone is the new-site default and the implicit default for
// CreateSite when the caller passes empty string. Replaces the legacy
// "Asia/Tehran" default that lived in the registry — IRST salt rotation
// in internal/identity/salt.go still uses Asia/Tehran (security
// invariant, unrelated to per-site UX TZ).
const DefaultTimezone = "Europe/Berlin"

// Timezones is the curated dropdown list. Every entry is asserted to
// resolve via time.LoadLocation in the build-time test
// (timezones_test.go). To extend: pick the IANA name from the tzdb,
// pick a short city Label, and add a row in the appropriate region
// block. The trailing UTC entry stays last for dropdown ergonomics.
var Timezones = []TimezoneOption{
	// Europe
	{IANA: "Europe/Berlin", Label: "Berlin"},
	{IANA: "Europe/London", Label: "London"},
	{IANA: "Europe/Paris", Label: "Paris"},
	{IANA: "Europe/Madrid", Label: "Madrid"},
	{IANA: "Europe/Amsterdam", Label: "Amsterdam"},
	{IANA: "Europe/Rome", Label: "Rome"},
	{IANA: "Europe/Warsaw", Label: "Warsaw"},
	{IANA: "Europe/Stockholm", Label: "Stockholm"},
	{IANA: "Europe/Athens", Label: "Athens"},
	{IANA: "Europe/Istanbul", Label: "Istanbul"},
	{IANA: "Europe/Moscow", Label: "Moscow"},
	// Americas
	{IANA: "America/New_York", Label: "New York"},
	{IANA: "America/Chicago", Label: "Chicago"},
	{IANA: "America/Denver", Label: "Denver"},
	{IANA: "America/Los_Angeles", Label: "Los Angeles"},
	{IANA: "America/Toronto", Label: "Toronto"},
	{IANA: "America/Vancouver", Label: "Vancouver"},
	{IANA: "America/Mexico_City", Label: "Mexico City"},
	{IANA: "America/Sao_Paulo", Label: "São Paulo"},
	{IANA: "America/Buenos_Aires", Label: "Buenos Aires"},
	// Asia / Middle East
	{IANA: "Asia/Dubai", Label: "Dubai"},
	{IANA: "Asia/Tehran", Label: "Tehran"},
	{IANA: "Asia/Riyadh", Label: "Riyadh"},
	{IANA: "Asia/Karachi", Label: "Karachi"},
	{IANA: "Asia/Kolkata", Label: "Kolkata"},
	{IANA: "Asia/Bangkok", Label: "Bangkok"},
	{IANA: "Asia/Singapore", Label: "Singapore"},
	{IANA: "Asia/Hong_Kong", Label: "Hong Kong"},
	{IANA: "Asia/Shanghai", Label: "Shanghai"},
	{IANA: "Asia/Tokyo", Label: "Tokyo"},
	{IANA: "Asia/Seoul", Label: "Seoul"},
	{IANA: "Asia/Jakarta", Label: "Jakarta"},
	// Oceania
	{IANA: "Australia/Sydney", Label: "Sydney"},
	{IANA: "Australia/Perth", Label: "Perth"},
	{IANA: "Pacific/Auckland", Label: "Auckland"},
	// Africa
	{IANA: "Africa/Johannesburg", Label: "Johannesburg"},
	{IANA: "Africa/Cairo", Label: "Cairo"},
	{IANA: "Africa/Lagos", Label: "Lagos"},
	// UTC
	{IANA: "UTC", Label: "UTC"},
}

// allowedTimezones + cachedLocations are built once at package init from
// Timezones. Hoisting time.LoadLocation out of the per-request hot path
// matters for /api/admin/timezones — without the cache, every GET walks
// the tzdata zip ~50× to compute current offsets.
var (
	allowedTimezones = func() map[string]struct{} {
		m := make(map[string]struct{}, len(Timezones))
		for _, t := range Timezones {
			m[t.IANA] = struct{}{}
		}

		return m
	}()

	cachedLocations = func() map[string]*time.Location {
		m := make(map[string]*time.Location, len(Timezones))
		for _, t := range Timezones {
			loc, err := time.LoadLocation(t.IANA)
			if err == nil && loc != nil {
				m[t.IANA] = loc
			}
		}

		return m
	}()
)

// IsValidTimezone reports whether the IANA name is in the allow-list AND
// resolves via time.LoadLocation. Both checks happen once at package
// init; this lookup is map-only.
func IsValidTimezone(iana string) bool {
	if _, ok := allowedTimezones[iana]; !ok {
		return false
	}

	_, ok := cachedLocations[iana]

	return ok
}

// TimezonesWithOffset returns Timezones with the Offset field populated
// from the live time.Location. now() is injected so tests can lock the
// clock; production callers pass time.Now. Locations come from
// cachedLocations so this is allocation + math only — no tzdata I/O.
func TimezonesWithOffset(now time.Time) []TimezoneOption {
	out := make([]TimezoneOption, len(Timezones))

	for i, t := range Timezones {
		out[i] = t

		loc, ok := cachedLocations[t.IANA]
		if !ok {
			out[i].Offset = "UTC"

			continue
		}

		out[i].Offset = formatOffset(now.In(loc))
	}

	return out
}

// formatOffset renders the local offset of t as "UTC+H" / "UTC-H:MM" /
// "UTC". The minute component is dropped when zero so the dropdown
// reads cleanly for whole-hour zones. Asia/Tehran (UTC+3:30) and
// Asia/Kolkata (UTC+5:30) are the only :30 zones in the curated list.
func formatOffset(t time.Time) string {
	_, secs := t.Zone()
	if secs == 0 {
		return "UTC"
	}

	sign := "+"
	if secs < 0 {
		sign = "-"
		secs = -secs
	}

	hours := secs / 3600
	minutes := (secs % 3600) / 60

	if minutes == 0 {
		return fmt.Sprintf("UTC%s%d", sign, hours)
	}

	return fmt.Sprintf("UTC%s%d:%02d", sign, hours, minutes)
}
