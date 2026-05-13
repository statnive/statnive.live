package privacy

import "testing"

func TestRoundToTen(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want int64 }{
		{-5, 0},
		{0, 0},
		{1, 10}, // sub-10 rounds up to 10
		{4, 10}, // upper boundary of sub-10
		{9, 10},
		{10, 10},
		{11, 10}, // round-half-up: 11 → 10
		{14, 10},
		{15, 20}, // round-half-up: 15 → 20
		{16, 20},
		{99, 100},
		{100, 100},
		{1234, 1230},
		{1235, 1240},
	}
	for _, c := range cases {
		if got := RoundToTen(c.in); got != c.want {
			t.Errorf("RoundToTen(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestRoundCountForMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		m    Mode
		in   int64
		want int64
	}{
		{ModeCurrent, 123, 123},
		{ModePermissive, 123, 123},
		{ModeConsentRequired, 123, 123},
		{ModeHybridPostConsent, 123, 123},
		{ModeConsentFree, 123, 120},
		{ModeHybridPreConsent, 127, 130},
	}
	for _, c := range cases {
		if got := RoundCountForMode(c.in, c.m); got != c.want {
			t.Errorf("RoundCountForMode(%d, %v) = %d, want %d", c.in, c.m, got, c.want)
		}
	}
}
