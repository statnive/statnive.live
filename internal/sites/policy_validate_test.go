package sites_test

import (
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/sites"
)

func TestSitePolicy_Validate_PinsTheKeyInvariants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		p       sites.SitePolicy
		wantErr string
	}{
		{
			name: "empty jurisdiction (migrated row) is fine",
			p:    sites.SitePolicy{},
		},
		{
			name: "OTHER-NON-EU + permissive (3 live operators) is fine",
			p:    sites.SitePolicy{Jurisdiction: "OTHER-NON-EU", ConsentMode: "permissive"},
		},
		{
			name: "DE + consent-free is fine",
			p:    sites.SitePolicy{Jurisdiction: "DE", ConsentMode: "consent-free"},
		},
		{
			name: "DE + hybrid + 1 allowlist entry is fine",
			p:    sites.SitePolicy{Jurisdiction: "DE", ConsentMode: "hybrid", EventAllowlist: []string{"pageview"}},
		},
		{
			name:    "DE + permissive rejected (TDDDG)",
			p:       sites.SitePolicy{Jurisdiction: "DE", ConsentMode: "permissive"},
			wantErr: "jurisdiction=DE requires consent_mode",
		},
		{
			name:    "hybrid outside EU rejected",
			p:       sites.SitePolicy{Jurisdiction: "IR", ConsentMode: "hybrid", EventAllowlist: []string{"pageview"}},
			wantErr: "consent_mode=hybrid only valid in EU jurisdictions",
		},
		{
			name: "hybrid in FR is fine",
			p: sites.SitePolicy{
				Jurisdiction:   "FR",
				ConsentMode:    "hybrid",
				EventAllowlist: []string{"pageview", "click"},
			},
		},
		{
			name:    "hybrid with 0 allowlist entries rejected (CNIL)",
			p:       sites.SitePolicy{Jurisdiction: "FR", ConsentMode: "hybrid"},
			wantErr: "hybrid requires 1-3 event_allowlist",
		},
		{
			name: "hybrid with 4 allowlist entries rejected (CNIL cap)",
			p: sites.SitePolicy{
				Jurisdiction:   "FR",
				ConsentMode:    "hybrid",
				EventAllowlist: []string{"pageview", "click", "scroll", "video_play"},
			},
			wantErr: "hybrid requires 1-3",
		},
		{
			name: "consent-free with 4 allowlist entries rejected (CNIL cap)",
			p: sites.SitePolicy{
				Jurisdiction:   "FR",
				ConsentMode:    "consent-free",
				EventAllowlist: []string{"pageview", "click", "scroll", "video_play"},
			},
			wantErr: "consent-free caps event_allowlist at 3",
		},
		{
			name:    "unknown jurisdiction rejected",
			p:       sites.SitePolicy{Jurisdiction: "ZW", ConsentMode: "permissive"},
			wantErr: `invalid jurisdiction "ZW"`,
		},
		{
			name:    "unknown consent_mode rejected",
			p:       sites.SitePolicy{Jurisdiction: "OTHER-NON-EU", ConsentMode: "bogus"},
			wantErr: `invalid consent_mode "bogus"`,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			err := c.p.Validate()
			if c.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("want error containing %q, got nil", c.wantErr)
			}

			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), c.wantErr)
			}
		})
	}
}

func TestDerivedConsentMode(t *testing.T) {
	t.Parallel()

	cases := []struct{ jurisdiction, want string }{
		{"DE", "consent-free"},
		{"FR", "consent-free"},
		{"OTHER-EU", "consent-free"},
		{"IR", "permissive"},
		{"OTHER-NON-EU", "permissive"},
		{"", "permissive"},
		{"unknown", "permissive"},
	}
	for _, c := range cases {
		if got := sites.DerivedConsentMode(c.jurisdiction); got != c.want {
			t.Errorf("DerivedConsentMode(%q) = %q, want %q", c.jurisdiction, got, c.want)
		}
	}
}
