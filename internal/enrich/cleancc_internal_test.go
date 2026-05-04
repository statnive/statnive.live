package enrich

import (
	"strings"
	"testing"
)

func TestCleanCountryCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"valid pass-through", "US", "US", true},
		{"lowercase coerced to upper", "us", "US", true},
		{"mixed case", "Us", "US", true},
		{"surrounding whitespace", " US ", "US", true},
		{"empty input", "", "--", false},
		{"single dash", "-", "--", false},
		{"already unknown", "--", "--", false},
		{"three letters rejected", "USA", "--", false},
		{"digits rejected", "12", "--", false},
		{"mixed digit-letter", "U1", "--", false},
		// 33-char fixture mirrors the production journal observation that
		// crashed every batch insert with "country_code input value with
		// length 33 exceeds FixedString(2) capacity".
		{"33-char overflow observed in prod", strings.Repeat("X", 33), "--", false},
		{"lite-db not-supported sentinel", ip2locationUnavailableSentinel, "--", false},
		{"tab and newline whitespace", "\tUS\n", "US", true},
		// 2-char accented Unicode would be 4 bytes UTF-8 — still rejected via
		// the length gate, but the byte-loop guards even if a future locale
		// coerces it to look like 2 chars somehow.
		{"two-byte multibyte rejected", "日本", "--", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := cleanCountryCode(tc.in)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("cleanCountryCode(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.wantOK)
			}

			if len(got) != 2 {
				t.Fatalf("cleanCountryCode(%q) returned %q (len %d); FixedString(2) requires exactly 2 bytes",
					tc.in, got, len(got))
			}
		})
	}
}
