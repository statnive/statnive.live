package storage

import (
	"strings"
	"testing"
	"time"
)

// TestGeoSortable_RevenueIsDefaultCompound asserts the geo panel's
// RPV-first sort renders the same compound expression as Sources /
// Campaigns. Regression guard if someone unifies the sort maps.
func TestGeoSortable_RevenueIsDefaultCompound(t *testing.T) {
	t.Parallel()

	got := geoSortable["revenue"]
	if got != "revenue, visitors" {
		t.Fatalf("geoSortable[revenue] = %q; want %q", got, "revenue, visitors")
	}
}

// TestGeoSortable_CountryDimension asserts the country sort key maps to
// the daily_geo column (country_code, FixedString(2)). The dashboard
// chip emits sort=country; we need to land on country_code, not the
// human-readable country name (which we don't carry).
func TestGeoSortable_CountryDimension(t *testing.T) {
	t.Parallel()

	got := geoSortable["country"]
	if got != "country_code" {
		t.Fatalf("geoSortable[country] = %q; want country_code", got)
	}
}

// TestGeoSortable_RejectsInjection asserts an attacker-supplied sort
// falls back to the default — same defense as orderClause_test.go but
// pinned to geoSortable specifically.
func TestGeoSortable_RejectsInjection(t *testing.T) {
	t.Parallel()

	f := &Filter{Sort: "; DROP TABLE statnive.daily_geo --"}

	got := orderClause(f, geoSortable, "revenue DESC, visitors DESC")
	if got != "ORDER BY revenue DESC, visitors DESC" {
		t.Fatalf("orderClause did not fall back; got %q", got)
	}
}

// TestApplyFilters_CountryNarrowsDailyGeo asserts a country chip on the
// Geo panel injects `AND country_code = ?` exactly once. Empty country
// must not inject anything.
func TestApplyFilters_CountryNarrowsDailyGeo(t *testing.T) {
	t.Parallel()

	baseWhere := "WHERE site_id = ? AND day >= ? AND day < ?"
	baseArgs := []any{uint32(801), time.Now(), time.Now()}

	t.Run("country set", func(t *testing.T) {
		t.Parallel()

		f := &Filter{Country: "IR"}

		where, args := applyFilters(f, baseWhere, baseArgs, dailyGeoCols)
		if !strings.Contains(where, "AND country_code = ?") {
			t.Fatalf("expected country_code predicate; got %q", where)
		}

		if got := strings.Count(where, "country_code = ?"); got != 1 {
			t.Fatalf("expected exactly one country_code predicate; got %d in %q", got, where)
		}

		if len(args) != len(baseArgs)+1 {
			t.Fatalf("expected one extra arg; got %d", len(args)-len(baseArgs))
		}

		if got, ok := args[len(args)-1].(string); !ok || got != "IR" {
			t.Fatalf("last arg = %v; want %q", args[len(args)-1], "IR")
		}
	})

	t.Run("country empty no-op", func(t *testing.T) {
		t.Parallel()

		f := &Filter{}

		where, args := applyFilters(f, baseWhere, baseArgs, dailyGeoCols)
		if where != baseWhere {
			t.Fatalf("empty country mutated WHERE: %q", where)
		}

		if len(args) != len(baseArgs) {
			t.Fatalf("empty country appended args: %v", args)
		}
	})
}

// TestApplyFilters_DailyGeoIgnoresChannelAndPath asserts the geo rollup
// allowlist refuses dimensions that don't live on daily_geo. A channel
// chip on the Geo panel is a no-op (we don't carry channel on the
// rollup); this prevents a SQL error from someone wiring through a
// shared chipsignal in a future refactor.
func TestApplyFilters_DailyGeoIgnoresChannelAndPath(t *testing.T) {
	t.Parallel()

	baseWhere := "WHERE site_id = ? AND day >= ? AND day < ?"
	f := &Filter{Channel: "Direct", Path: "/foo"}

	where, args := applyFilters(f, baseWhere, []any{}, dailyGeoCols)
	if where != baseWhere {
		t.Fatalf("daily_geo allowlist leaked non-rollup column: %q", where)
	}

	if len(args) != 0 {
		t.Fatalf("daily_geo allowlist leaked args: %v", args)
	}
}

// TestGeoTopCountriesLimit_IsConstant pins the server-side ceiling to
// 25. The dashboard slices to top-10 per axis and collapses the rest
// into "Other"; bumping the constant is fine but must be intentional.
func TestGeoTopCountriesLimit_IsConstant(t *testing.T) {
	t.Parallel()

	if geoTopCountriesLimit != 25 {
		t.Fatalf("geoTopCountriesLimit = %d; want 25 (see GeoTopCountries comment)", geoTopCountriesLimit)
	}
}
