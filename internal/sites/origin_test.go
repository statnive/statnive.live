package sites_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/sites"
)

// TestNormalizeOrigin pins the per-entry RFC 6454 validation that
// CORS middleware and OriginIndex both depend on.
func TestNormalizeOrigin(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
		err  bool
	}{
		{name: "plain https origin", in: "https://www.televika.com", want: "https://www.televika.com"},
		{name: "uppercased host lowercased", in: "https://Televika.COM", want: "https://televika.com"},
		{name: "explicit port preserved", in: "https://staging.televika.com:8443", want: "https://staging.televika.com:8443"},
		{name: "trailing slash stripped", in: "https://televika.com/", want: "https://televika.com"},
		{name: "leading whitespace trimmed", in: "  https://televika.com  ", want: "https://televika.com"},
		{name: "IDN punycoded", in: "https://тelevika.com", want: "https://xn--elevika-5jg.com"},
		{name: "empty rejected", in: "", err: true},
		{name: "literal null rejected", in: "null", err: true},
		{name: "http rejected (must be https)", in: "http://televika.com", err: true},
		{name: "wildcard rejected", in: "*", err: true},
		{name: "subdomain wildcard rejected", in: "https://*.televika.com", err: true},
		{name: "path rejected", in: "https://televika.com/api", err: true},
		{name: "query rejected", in: "https://televika.com?foo=bar", err: true},
		{name: "fragment rejected", in: "https://televika.com#frag", err: true},
		{name: "userinfo rejected", in: "https://user:pass@televika.com", err: true},
		{name: "no scheme rejected", in: "televika.com", err: true},
		{name: "ws scheme rejected", in: "ws://televika.com", err: true},
		{name: "file scheme rejected", in: "file:///etc/passwd", err: true},
		{name: "data scheme rejected", in: "data:text/html;base64,PHA+", err: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := sites.NormalizeOrigin(c.in)

			switch {
			case c.err && err == nil:
				t.Fatalf("NormalizeOrigin(%q) = %q, want error", c.in, got)
			case !c.err && err != nil:
				t.Fatalf("NormalizeOrigin(%q) error: %v", c.in, err)
			case !c.err && got != c.want:
				t.Errorf("NormalizeOrigin(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestNormalizeOrigin_IDN_Collision — visually-identical IDN inputs
// (Cyrillic vs Latin) MUST remain distinct after normalization, while
// case-variants of the same script MUST converge.
func TestNormalizeOrigin_IDN_Collision(t *testing.T) {
	t.Parallel()

	// Cyrillic 'т' vs Latin 't' renders identically; the bug we
	// want to catch is two admins registering the visually-same
	// "televika.com" while the bytes differ.
	cyrillic, err := sites.NormalizeOrigin("https://тelevika.com")
	if err != nil {
		t.Fatalf("punycode normalize failed: %v", err)
	}

	latin, err := sites.NormalizeOrigin("https://televika.com")
	if err != nil {
		t.Fatalf("ascii normalize failed: %v", err)
	}

	if cyrillic == latin {
		t.Errorf("cyrillic %q must NOT equal latin %q (distinct domains under IDN)", cyrillic, latin)
	}

	// But the same cyrillic in two case-variants MUST converge.
	upper, err := sites.NormalizeOrigin("https://ТELEVIKA.com")
	if err != nil {
		t.Fatalf("uppercase IDN failed: %v", err)
	}

	if upper != cyrillic {
		t.Errorf("case-variants must converge: %q vs %q", upper, cyrillic)
	}
}

// TestPolicy_Validate_AllowedOrigins owns the AllowedOrigins-side
// Validate invariants; jurisdiction × consent_mode invariants live in
// policy_validate_test.go.
func TestPolicy_Validate_AllowedOrigins(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		origins []string
		wantErr string
	}{
		{name: "empty list ok"},
		{name: "single valid origin", origins: []string{"https://televika.com"}},
		{
			name:    "multiple valid origins",
			origins: []string{"https://televika.com", "https://www.televika.com", "https://staging.televika.com:8443"},
		},
		{
			name:    "11 entries rejected (cap is 10)",
			origins: make11Origins(),
			wantErr: "allowed_origins capped at 10",
		},
		{
			name:    "invalid scheme rejected",
			origins: []string{"http://televika.com"},
			wantErr: "invalid origin",
		},
		{
			name:    "wildcard rejected",
			origins: []string{"*"},
			wantErr: "invalid origin",
		},
		{
			name:    "with path rejected",
			origins: []string{"https://televika.com/api"},
			wantErr: "invalid origin",
		},
		{
			name:    "literal null rejected",
			origins: []string{"null"},
			wantErr: "invalid origin",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			p := sites.SitePolicy{AllowedOrigins: c.origins}
			err := p.Validate()

			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("Validate error: %v", err)
			case c.wantErr != "" && err == nil:
				t.Fatalf("Validate: want error containing %q, got nil", c.wantErr)
			case c.wantErr != "" && err != nil:
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("Validate error = %q, want substring %q", err.Error(), c.wantErr)
				}
			}
		})
	}
}

func make11Origins() []string {
	out := make([]string, 0, 11)

	for i := range 11 {
		out = append(out, fmt.Sprintf("https://op%d.example.com", i))
	}

	return out
}
