package timewindow

import (
	"errors"
	"testing"
	"time"
)

// fixedNow is a deterministic reference instant: 2026-06-04 13:30:00 UTC.
var fixedNow = time.Date(2026, 6, 4, 13, 30, 0, 0, time.UTC)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()

	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}

	return loc
}

func TestParseRange_DayShorthand_UTC(t *testing.T) {
	t.Parallel()

	from, to, err := ParseRange("7d", time.UTC, fixedNow)
	if err != nil {
		t.Fatalf("ParseRange: %v", err)
	}

	// to = tomorrow UTC midnight (2026-06-05); from = to - 7d (2026-05-29).
	wantTo := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	wantFrom := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)

	if !to.Equal(wantTo) {
		t.Errorf("to = %s, want %s", to, wantTo)
	}

	if !from.Equal(wantFrom) {
		t.Errorf("from = %s, want %s", from, wantFrom)
	}

	if to.Sub(from) != 7*24*time.Hour {
		t.Errorf("span = %s, want 168h", to.Sub(from))
	}
}

func TestParseRange_EmptyDefaultsTo7d(t *testing.T) {
	t.Parallel()

	a1, b1, err1 := ParseRange("", time.UTC, fixedNow)
	a2, b2, err2 := ParseRange("7d", time.UTC, fixedNow)

	if err1 != nil || err2 != nil {
		t.Fatalf("errs: %v / %v", err1, err2)
	}

	if !a1.Equal(a2) || !b1.Equal(b2) {
		t.Errorf("empty range != 7d: [%s,%s) vs [%s,%s)", a1, b1, a2, b2)
	}
}

func TestParseRange_DayShorthand_TehranHalfHourZone(t *testing.T) {
	t.Parallel()

	// Asia/Tehran is UTC+3:30 — local midnight is 20:30 UTC the prior day.
	// At fixedNow (2026-06-04 13:30 UTC = 17:00 Tehran), "today" in Tehran
	// is 2026-06-04, so tomorrow-local-midnight = 2026-06-05 00:00 +03:30
	// = 2026-06-04 20:30 UTC.
	tehran := mustLoad(t, "Asia/Tehran")

	from, to, err := ParseRange("1d", tehran, fixedNow)
	if err != nil {
		t.Fatalf("ParseRange: %v", err)
	}

	wantTo := time.Date(2026, 6, 4, 20, 30, 0, 0, time.UTC)
	wantFrom := wantTo.AddDate(0, 0, -1)

	if !to.Equal(wantTo) {
		t.Errorf("to = %s, want %s (Tehran local midnight in UTC)", to.UTC(), wantTo)
	}

	if !from.Equal(wantFrom) {
		t.Errorf("from = %s, want %s", from.UTC(), wantFrom)
	}
}

func TestParseRange_HourShorthand_AlignsToUTCHour(t *testing.T) {
	t.Parallel()

	// 1h with a half-hour zone must still align to the UTC rollup hour,
	// NOT local time. fixedNow = 13:30 UTC → current hour [13:00,14:00).
	for _, loc := range []*time.Location{time.UTC, mustLoad(t, "Asia/Tehran")} {
		from, to, err := ParseRange("1h", loc, fixedNow)
		if err != nil {
			t.Fatalf("ParseRange(1h, %s): %v", loc, err)
		}

		wantFrom := time.Date(2026, 6, 4, 13, 0, 0, 0, time.UTC)
		wantTo := time.Date(2026, 6, 4, 14, 0, 0, 0, time.UTC)

		if !from.Equal(wantFrom) || !to.Equal(wantTo) {
			t.Errorf("loc=%s 1h = [%s,%s), want [%s,%s)", loc, from, to, wantFrom, wantTo)
		}
	}
}

func TestParseRange_24h(t *testing.T) {
	t.Parallel()

	from, to, err := ParseRange("24h", time.UTC, fixedNow)
	if err != nil {
		t.Fatalf("ParseRange: %v", err)
	}

	wantTo := time.Date(2026, 6, 4, 14, 0, 0, 0, time.UTC)
	if !to.Equal(wantTo) || to.Sub(from) != 24*time.Hour {
		t.Errorf("24h = [%s,%s), want 24h ending %s", from, to, wantTo)
	}
}

func TestParseRange_ExplicitEndExclusive(t *testing.T) {
	t.Parallel()

	from, to, err := ParseRange("2026-04-01..2026-04-18", time.UTC, fixedNow)
	if err != nil {
		t.Fatalf("ParseRange: %v", err)
	}

	wantFrom := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC)

	if !from.Equal(wantFrom) || !to.Equal(wantTo) {
		t.Errorf("explicit = [%s,%s), want [%s,%s)", from, to, wantFrom, wantTo)
	}
}

func TestParseRange_Rejections(t *testing.T) {
	t.Parallel()

	bad := []string{
		"7x",                     // bad unit
		"0d",                     // non-positive
		"-3d",                    // negative
		"9999d",                  // exceeds 365-day cap
		"d",                      // no number
		"2026-04-18..2026-04-01", // end before start
		"2026-04-01..2026-04-01", // zero-width
		"notadate..2026-04-01",   // unparseable side
		"2026-04-01..",           // missing side
		"abc",                    // garbage
	}

	for _, in := range bad {
		if _, _, err := ParseRange(in, time.UTC, fixedNow); !errors.Is(err, ErrBadRange) {
			t.Errorf("ParseRange(%q) err = %v, want ErrBadRange", in, err)
		}
	}
}

func TestParseDateRange_DefaultsMatchDashboard(t *testing.T) {
	t.Parallel()

	// Empty from+to → [tomorrow-7d, tomorrow) local midnight, UTC.
	from, to, err := ParseDateRange("", "", time.UTC, fixedNow)
	if err != nil {
		t.Fatalf("ParseDateRange: %v", err)
	}

	wantTo := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	wantFrom := wantTo.AddDate(0, 0, -DefaultRangeDays)

	if !to.Equal(wantTo) || !from.Equal(wantFrom) {
		t.Errorf("defaults = [%s,%s), want [%s,%s)", from, to, wantFrom, wantTo)
	}
}

func TestParseDateRange_ExplicitDates(t *testing.T) {
	t.Parallel()

	from, to, err := ParseDateRange("2026-01-01", "2026-01-08", time.UTC, fixedNow)
	if err != nil {
		t.Fatalf("ParseDateRange: %v", err)
	}

	if to.Sub(from) != 7*24*time.Hour {
		t.Errorf("span = %s, want 168h", to.Sub(from))
	}
}

func TestParseLocalDate(t *testing.T) {
	t.Parallel()

	zero, err := ParseLocalDate("", time.UTC)
	if err != nil || !zero.IsZero() {
		t.Errorf("empty: got (%s, %v), want (zero, nil)", zero, err)
	}

	if _, err := ParseLocalDate("2026-13-99", time.UTC); !errors.Is(err, ErrBadRange) {
		t.Errorf("invalid date err = %v, want ErrBadRange", err)
	}
}
