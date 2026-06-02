package storage

import (
	"math"
	"testing"
)

// scipy parity reference (regenerate via:
//
//	from scipy.stats import proportions_ztest, ... ).
//
// proportions_ztest uses pooled variance under H0 by default, which
// matches our implementation. Hand-picked cases span:
//   - identical proportions (z=0, p=1)
//   - small effect, large n  (clear win)
//   - large effect, small n  (tight)
//   - one-side-zero (degenerate)
//   - both-zero (degenerate)
//
// Tolerance is 1e-6 — math.Erfc and scipy's normal-CDF agree to
// double-precision well past that.
func TestTwoProportionZTestPValue_ScipyParity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		kA, nA, kB, nB uint64
		wantP          float64
	}{
		{
			name: "identical proportions → p=1",
			kA:   25, nA: 100, kB: 50, nB: 200,
			wantP: 1.0,
		},
		{
			name: "60/40 split at 100 visitors each, kB - kA = 8 → moderate p",
			// p̂_A = 0.30, p̂_B = 0.38, p̂_pool = 0.34, SE ≈ 0.0670
			// z ≈ 1.195, two-tailed p ≈ 0.232414 (matches statsmodels
			// proportions_ztest(count=[30,38], nobs=[100,100])).
			kA: 30, nA: 100, kB: 38, nB: 100,
			wantP: 0.232414264917081,
		},
		{
			name: "clear win at large n: 250/10k vs 410/10k",
			// p̂_A = 0.025, p̂_B = 0.041, p̂_pool = 0.033
			// z ≈ 6.346, p ≈ 2.40e-10 (matches statsmodels).
			kA: 250, nA: 10000, kB: 410, nB: 10000,
			wantP: 2.39865293180734e-10,
		},
		{
			name: "nA == 0 → p=1 (no inference)",
			kA:   0, nA: 0, kB: 5, nB: 100,
			wantP: 1.0,
		},
		{
			name: "all-zero conversions → p=1 (pPool=0, no variance)",
			kA:   0, nA: 100, kB: 0, nB: 100,
			wantP: 1.0,
		},
		{
			name: "all-converted → p=1 (pPool=1, no variance)",
			kA:   100, nA: 100, kB: 100, nB: 100,
			wantP: 1.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := twoProportionZTestPValue(tc.kA, tc.nA, tc.kB, tc.nB)
			diff := math.Abs(got - tc.wantP)

			// For p near 1 the absolute tolerance is fine.
			// For tiny p (1e-10) we want relative tolerance.
			tol := 1e-6
			if tc.wantP < 1e-3 && tc.wantP > 0 {
				tol = tc.wantP * 1e-4
			}

			if diff > tol {
				t.Errorf("twoProportionZTestPValue() = %g, want %g (diff %g, tol %g)",
					got, tc.wantP, diff, tol)
			}
		})
	}
}

// Wilson CI parity reference: scipy.stats.binom.interval is the
// Clopper-Pearson interval, NOT Wilson. We instead cross-check against
// the closed-form Wilson formula on the canonical Newcombe 1998 cases.
func TestWilsonCI_KnownFixtures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		k, n           uint64
		wantLo, wantHi float64
	}{
		// Canonical no-continuity-correction Wilson formula values
		// (matches scipy.stats: proportion_confint(... method='wilson')).
		// References citing slightly different values (e.g. 0.27755) use
		// a continuity-corrected variant — we use the canonical Wilson.
		{name: "0/10", k: 0, n: 10, wantLo: 0.0, wantHi: 0.2775328},
		{name: "5/10", k: 5, n: 10, wantLo: 0.2365931, wantHi: 0.7634069},
		{name: "50/100", k: 50, n: 100, wantLo: 0.4038315, wantHi: 0.5961685},
		{name: "412/10000", k: 412, n: 10000, wantLo: 0.0374775, wantHi: 0.0452749},
		{name: "n=0", k: 0, n: 0, wantLo: 0.0, wantHi: 0.0},
		{name: "10/10", k: 10, n: 10, wantLo: 0.7224672, wantHi: 1.0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lo, hi := wilsonCI(tc.k, tc.n)
			if math.Abs(lo-tc.wantLo) > 1e-5 {
				t.Errorf("wilsonCI lo = %g, want %g", lo, tc.wantLo)
			}

			if math.Abs(hi-tc.wantHi) > 1e-5 {
				t.Errorf("wilsonCI hi = %g, want %g", hi, tc.wantHi)
			}
		})
	}
}

// TestApplyVariantStats_SampleSizeGuard pins the dual threshold from
// research § 5: significance is withheld when n<100 OR conversions<25
// on EITHER the control or the variant.
func TestApplyVariantStats_SampleSizeGuard(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		control   VariantRow
		variant   VariantRow
		wantStats bool
	}{
		{
			name:      "both meet thresholds",
			control:   VariantRow{Value: "A", Visitors: 100, GoalCompletions: 25, ConversionRate: 0.25},
			variant:   VariantRow{Value: "B", Visitors: 100, GoalCompletions: 30, ConversionRate: 0.30},
			wantStats: true,
		},
		{
			name:      "control below visitor threshold",
			control:   VariantRow{Value: "A", Visitors: 99, GoalCompletions: 25, ConversionRate: 0.25},
			variant:   VariantRow{Value: "B", Visitors: 1000, GoalCompletions: 300, ConversionRate: 0.30},
			wantStats: false,
		},
		{
			name:      "variant below conversion threshold",
			control:   VariantRow{Value: "A", Visitors: 1000, GoalCompletions: 300, ConversionRate: 0.30},
			variant:   VariantRow{Value: "B", Visitors: 100, GoalCompletions: 24, ConversionRate: 0.24},
			wantStats: false,
		},
		{
			name:      "exact threshold passes",
			control:   VariantRow{Value: "A", Visitors: 100, GoalCompletions: 25, ConversionRate: 0.25},
			variant:   VariantRow{Value: "B", Visitors: 100, GoalCompletions: 25, ConversionRate: 0.25},
			wantStats: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			variants := []VariantRow{tc.control, tc.variant}
			applyVariantStats(&variants, tc.control)

			// Variant row is index 1 (control is 0).
			got := variants[1].PValue != nil && variants[1].Significant != nil

			if got != tc.wantStats {
				t.Errorf("stats populated = %v, want %v (PValue=%v, Significant=%v)",
					got, tc.wantStats, variants[1].PValue, variants[1].Significant)
			}
		})
	}
}

// TestApplyVariantStats_ControlRow asserts the control row carries
// only the CI (lo/hi) and never delta/p-value/significant — the UI
// renders "—" for those columns on the control.
func TestApplyVariantStats_ControlRow(t *testing.T) {
	t.Parallel()

	control := VariantRow{Value: "A", Visitors: 200, GoalCompletions: 50, ConversionRate: 0.25}
	variant := VariantRow{Value: "B", Visitors: 200, GoalCompletions: 60, ConversionRate: 0.30}

	variants := []VariantRow{control, variant}
	applyVariantStats(&variants, control)

	if variants[0].DeltaPP != nil {
		t.Errorf("control row DeltaPP should be nil, got %v", *variants[0].DeltaPP)
	}

	if variants[0].PValue != nil {
		t.Errorf("control row PValue should be nil, got %v", *variants[0].PValue)
	}

	if variants[0].CILow == nil || variants[0].CIHigh == nil {
		t.Errorf("control row CI should be populated, got %v/%v", variants[0].CILow, variants[0].CIHigh)
	}
}

// TestScopeToColumn pins the wire-scope → events_raw column mapping.
// Unknown scope returns "" so callers reject with HTTP 400.
func TestScopeToColumn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		scope string
		want  string
	}{
		{"hit", "hit_props"},
		{"session", "session_props"},
		{"user", "user_props"},
		{"", ""},
		{"unknown", ""},
		{"HIT", ""}, // case-sensitive on purpose
	}

	for _, tc := range cases {
		if got := scopeToColumn(tc.scope); got != tc.want {
			t.Errorf("scopeToColumn(%q) = %q, want %q", tc.scope, got, tc.want)
		}
	}
}
