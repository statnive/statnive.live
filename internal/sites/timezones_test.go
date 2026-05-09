package sites

import (
	"strings"
	"testing"
	"time"
)

// TestTimezones_AllResolveLoadLocation asserts every entry in the
// public Timezones slice resolves via time.LoadLocation. Catches typos
// at test time, not at runtime when the dashboard tries to interpret
// a date range and falls through to UTC.
func TestTimezones_AllResolveLoadLocation(t *testing.T) {
	t.Parallel()

	if len(Timezones) == 0 {
		t.Fatal("Timezones slice is empty")
	}

	if len(allowedTimezones) != len(Timezones) {
		t.Errorf("allowedTimezones len = %d, Timezones len = %d", len(allowedTimezones), len(Timezones))
	}

	for _, tz := range Timezones {
		if _, err := time.LoadLocation(tz.IANA); err != nil {
			t.Errorf("Timezones[%q]: LoadLocation failed: %v", tz.IANA, err)
		}

		if !IsValidTimezone(tz.IANA) {
			t.Errorf("Timezones[%q] missing from allowedTimezones", tz.IANA)
		}

		if tz.Label == "" {
			t.Errorf("Timezones[%q] has empty Label", tz.IANA)
		}
	}
}

// TestIsValidTimezone_Rejects asserts the negative path: zones outside
// the allow-list, or syntactically valid IANA names that aren't on
// the curated dropdown list, are rejected.
func TestIsValidTimezone_Rejects(t *testing.T) {
	t.Parallel()

	for _, tz := range []string{"", "Mars/Olympus", "Atlantis/Capital", "europe/berlin", "Europe/berlin"} {
		if IsValidTimezone(tz) {
			t.Errorf("IsValidTimezone(%q) = true, want false", tz)
		}
	}
}

// TestDefaultTimezone_IsValid catches the regression where someone
// renames DefaultTimezone without updating the Timezones slice.
// CreateSite would then return ErrInvalidTimezone for the very
// default it falls back to.
func TestDefaultTimezone_IsValid(t *testing.T) {
	t.Parallel()

	if !IsValidTimezone(DefaultTimezone) {
		t.Errorf("DefaultTimezone %q not in allow-list", DefaultTimezone)
	}

	if DefaultTimezone != "Europe/Berlin" {
		t.Errorf("DefaultTimezone = %q, want Europe/Berlin (per design decision)", DefaultTimezone)
	}
}

// TestTimezonesWithOffset_FormatsCorrectly pins the Offset format
// against fixed clocks for both whole-hour and half-hour zones. The
// SPA dropdown depends on this string format ("UTC+1", "UTC+3:30",
// "UTC-5", "UTC") for stable rendering.
func TestTimezonesWithOffset_FormatsCorrectly(t *testing.T) {
	t.Parallel()

	// 2026-01-15 12:00 UTC — winter; Berlin is CET (UTC+1), NY is EST (UTC-5).
	winter := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	winterOpts := TimezonesWithOffset(winter)

	got := offsetFor(winterOpts, "Europe/Berlin")
	if got != "UTC+1" {
		t.Errorf("Berlin winter offset = %q, want UTC+1", got)
	}

	got = offsetFor(winterOpts, "America/New_York")
	if got != "UTC-5" {
		t.Errorf("NY winter offset = %q, want UTC-5", got)
	}

	got = offsetFor(winterOpts, "Asia/Tehran")
	if !strings.HasPrefix(got, "UTC+3:30") {
		t.Errorf("Tehran offset = %q, want UTC+3:30 (no DST since 2022)", got)
	}

	got = offsetFor(winterOpts, "UTC")
	if got != "UTC" {
		t.Errorf("UTC offset = %q, want UTC", got)
	}

	// 2026-07-15 12:00 UTC — summer; Berlin is CEST (UTC+2), NY is EDT (UTC-4).
	summer := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	summerOpts := TimezonesWithOffset(summer)

	got = offsetFor(summerOpts, "Europe/Berlin")
	if got != "UTC+2" {
		t.Errorf("Berlin summer offset = %q, want UTC+2", got)
	}

	got = offsetFor(summerOpts, "America/New_York")
	if got != "UTC-4" {
		t.Errorf("NY summer offset = %q, want UTC-4", got)
	}
}

func offsetFor(opts []TimezoneOption, iana string) string {
	for _, o := range opts {
		if o.IANA == iana {
			return o.Offset
		}
	}

	return ""
}
