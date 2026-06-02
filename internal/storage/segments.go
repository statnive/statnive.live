package storage

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// scopeToColumn maps the segments wire-scope name to the events_raw
// Map column. Unknown scopes return "" so callers can reject the
// request with a 400 rather than building invalid SQL.
func scopeToColumn(scope string) string {
	switch scope {
	case "hit":
		return "hit_props"
	case "session":
		return "session_props"
	case "user":
		return "user_props"
	}

	return ""
}

// PropNames returns up to `limit` distinct prop names from the given
// scope's Map column on events_raw. Live scan with a tight 7-day
// window + SAMPLE rate for v1; v1.1 promotes this to a nightly-
// refreshed prop_name_cache MV per the plan's § 6 spec.
//
// Architecture Rule 1: events_raw read is the same carve-out that
// whereWithProps documents — bounded scan window + cache wrapper
// (CachedStore wraps the Store at the http layer with TTL 24h).
//
// Tenant-isolation: WHERE site_id = ? is the first clause per
// Architecture Rule 8. The handler enforces the actor's grant on
// f.SiteID before this method is called.
func (s *clickhouseStore) PropNames(ctx context.Context, f *Filter, scope string, limit int) ([]PropNameRow, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	col := scopeToColumn(scope)
	if col == "" {
		return nil, fmt.Errorf("%w: scope %q must be hit|session|user", ErrInvalidFilter, scope)
	}

	if limit <= 0 || limit > 200 {
		limit = 100
	}

	q := fmt.Sprintf(`
		SELECT
			name,
			arrayDistinct(groupArray(value)) AS sample_values,
			max(time) AS last_seen
		FROM (
			SELECT
				time,
				arrayJoin(mapKeys(%s)) AS name,
				%s[arrayJoin(mapKeys(%s))] AS value
			FROM statnive.events_raw
			WHERE site_id = ?
			  AND time >= now() - INTERVAL 7 DAY
			SAMPLE 0.1
		)
		GROUP BY name
		ORDER BY last_seen DESC
		LIMIT %d
		SETTINGS max_execution_time = 10, max_memory_usage = 4294967296
	`, col, col, col, limit)

	rows, err := s.conn.Query(ctx, q, f.SiteID)
	if err != nil {
		return nil, fmt.Errorf("prop_names query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := make([]PropNameRow, 0, limit)

	for rows.Next() {
		var (
			name     string
			values   []string
			lastSeen time.Time
		)

		if scanErr := rows.Scan(&name, &values, &lastSeen); scanErr != nil {
			return nil, fmt.Errorf("prop_names scan: %w", scanErr)
		}

		// Truncate sample list — the chip's value-picker only needs ~50.
		if len(values) > 50 {
			values = values[:50]
		}

		out = append(out, PropNameRow{Name: name, SampleValues: values, LastSeen: lastSeen})
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("prop_names iter: %w", rowsErr)
	}

	return out, nil
}

// minVariantVisitors and minVariantConversions are the sample-size
// guard thresholds. Per research § 5: Optimizely + VWO consensus is
// n >= 100 visitors AND >= 25 conversions per variant. Below either,
// the math layer withholds p-value/significant/CI so the UI renders
// the sample-warning row instead of a misleading "SIG" badge.
const (
	minVariantVisitors    = uint64(100)
	minVariantConversions = uint64(25)
)

// Compare implements /api/stats/compare. Pivots the variant table for
// an A/B style experiment: pick a dimension (scope:name), pick a goal
// event_name, return one row per distinct value of the dimension with
// visitor count + goal completions + conversion rate + uncertainty
// (vs control). Phase 4 of segments.
//
// Math (research §§ 3, 4, 5):
//   - p̂_pool = (k_A + k_B) / (n_A + n_B); SE_pool = sqrt(p̂(1-p̂)(1/n_A + 1/n_B))
//   - z = (p̂_B - p̂_A) / SE_pool, two-tailed normal-CDF p-value
//   - Wilson CI for each variant's rate (Agresti-Coull/Newcombe 1998)
//   - Significance withheld when n<100 OR k<25 per variant
//
// Architecture Rule 1: events_raw scan. Same raw-fallback carve-out
// as overviewFromRaw + cached 1h by CachedStore.
func (s *clickhouseStore) Compare(ctx context.Context, f *Filter, dimension, goal string) (*CompareResult, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	scope, name, ok := strings.Cut(dimension, ":")
	if !ok {
		return nil, fmt.Errorf("%w: dimension %q must be <scope>:<name>", ErrInvalidFilter, dimension)
	}

	col := scopeToColumn(scope)
	if col == "" {
		return nil, fmt.Errorf("%w: dimension scope %q must be hit|session|user", ErrInvalidFilter, scope)
	}

	if goal == "" {
		return nil, fmt.Errorf("%w: goal is required", ErrInvalidFilter)
	}

	q := fmt.Sprintf(`
		SELECT
			%s[?]                                            AS value,
			toUInt64(uniqCombined64(visitor_hash))           AS visitors,
			toUInt64(countIf(is_goal = 1 AND event_name = ?)) AS goal_completions
		FROM statnive.events_raw
		WHERE site_id = ?
		  AND time >= ?
		  AND time < ?
		  AND has(%s, ?)
		GROUP BY value
		ORDER BY visitors DESC
		LIMIT 25
		SETTINGS max_execution_time = 30, max_memory_usage = 8589934592
	`, col, col)

	args := []any{name, goal, f.SiteID, f.From, f.To, name}

	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("compare query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var variants []VariantRow

	for rows.Next() {
		var v VariantRow
		if scanErr := rows.Scan(&v.Value, &v.Visitors, &v.GoalCompletions); scanErr != nil {
			return nil, fmt.Errorf("compare scan: %w", scanErr)
		}

		if v.Visitors > 0 {
			v.ConversionRate = float64(v.GoalCompletions) / float64(v.Visitors)
		}

		variants = append(variants, v)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("compare iter: %w", rowsErr)
	}

	res := &CompareResult{Dimension: dimension, Goal: goal, Variants: variants}

	if len(variants) == 0 {
		return res, nil
	}

	// Stable ordering — control is the row with most visitors; on tie
	// fall back to lexicographic so different callers see the same row 0.
	sort.SliceStable(variants, func(i, j int) bool {
		if variants[i].Visitors != variants[j].Visitors {
			return variants[i].Visitors > variants[j].Visitors
		}

		return variants[i].Value < variants[j].Value
	})

	control := variants[0]
	res.Control = control.Value

	applyVariantStats(&variants, control)

	res.Variants = variants

	return res, nil
}

// applyVariantStats populates DeltaPP / DeltaRel / PValue / Significant /
// CILow / CIHigh on every variant (except the control row, which only
// carries CI). The control row's significance fields stay nil so the
// UI can render the "—" placeholder per § 11.4 of the plan.
//
// Pulled out of Compare so each math step is unit-testable in
// isolation without a ClickHouse fixture (see segments_test.go).
func applyVariantStats(variants *[]VariantRow, control VariantRow) {
	for i := range *variants {
		v := &(*variants)[i]
		ciLo, ciHi := wilsonCI(v.GoalCompletions, v.Visitors)
		v.CILow = &ciLo
		v.CIHigh = &ciHi

		if v.Value == control.Value {
			continue
		}

		if !hasMinimumSample(*v) || !hasMinimumSample(control) {
			continue
		}

		dPP := v.ConversionRate - control.ConversionRate
		v.DeltaPP = &dPP

		if control.ConversionRate > 0 {
			dRel := dPP / control.ConversionRate
			v.DeltaRel = &dRel
		}

		p := twoProportionZTestPValue(
			control.GoalCompletions, control.Visitors,
			v.GoalCompletions, v.Visitors,
		)
		v.PValue = &p

		sig := p < 0.05
		v.Significant = &sig
	}
}

func hasMinimumSample(v VariantRow) bool {
	return v.Visitors >= minVariantVisitors && v.GoalCompletions >= minVariantConversions
}

// wilsonCI returns the 95% Wilson score interval for k successes in n
// trials. More accurate than Wald (normal-approx) for extreme p and
// small n; recommended default in the binomial-proportion literature
// (NIST handbook + Agresti-Coull 1998 + Newcombe 1998).
//
// Returns (lo, hi) clipped to [0, 1]. n == 0 returns (0, 0).
func wilsonCI(k, n uint64) (lo, hi float64) {
	if n == 0 {
		return 0, 0
	}

	const z = 1.959963984540054 // 1.96 for 95% confidence

	p := float64(k) / float64(n)
	fn := float64(n)

	denom := 1 + (z*z)/fn
	center := (p + (z*z)/(2*fn)) / denom
	margin := (z / denom) * math.Sqrt((p*(1-p)/fn)+((z*z)/(4*fn*fn)))

	lo = center - margin
	hi = center + margin

	if lo < 0 {
		lo = 0
	}

	if hi > 1 {
		hi = 1
	}

	return lo, hi
}

// twoProportionZTestPValue runs a two-tailed two-proportion z-test
// with pooled variance under H0 (the canonical form used by
// statsmodels.stats.proportion.proportions_ztest). Returns the
// two-tailed p-value via math.Erfc on the absolute z-statistic.
//
// kA / nA: control. kB / nB: variant. Either nA == 0 or nB == 0
// returns 1 (no inference possible). p̂_pool == 0 or 1 returns 1
// (zero variance, no test possible).
func twoProportionZTestPValue(kA, nA, kB, nB uint64) float64 {
	if nA == 0 || nB == 0 {
		return 1
	}

	pA := float64(kA) / float64(nA)
	pB := float64(kB) / float64(nB)

	pPool := float64(kA+kB) / float64(nA+nB)
	if pPool <= 0 || pPool >= 1 {
		return 1
	}

	se := math.Sqrt(pPool * (1 - pPool) * (1/float64(nA) + 1/float64(nB)))
	if se == 0 {
		return 1
	}

	z := (pB - pA) / se

	// two-tailed p = 2 * (1 - Φ(|z|)) = erfc(|z|/sqrt(2))
	return math.Erfc(math.Abs(z) / math.Sqrt2)
}
