package enrich_test

import (
	"net/netip"
	"testing"

	"github.com/statnive/statnive.live/internal/enrich"
)

func TestBotDetector_EmptyUAIsBot(t *testing.T) {
	t.Parallel()

	b := enrich.NewBotDetector(discardLogger())
	got, reason := b.IsBot("", "")

	if !got {
		t.Error("empty UA should be flagged as bot")
	}

	if reason != "empty_ua" {
		t.Errorf("reason = %q, want empty_ua", reason)
	}
}

func TestBotDetector_LiteralMatch(t *testing.T) {
	t.Parallel()

	b := enrich.NewBotDetector(discardLogger())

	cases := []struct {
		ua   string
		want bool
	}{
		{"Mozilla/5.0 (compatible; Googlebot/2.1)", true},
		{"Mozilla/5.0 (compatible; bingbot/2.0)", true},
		{"facebookexternalhit/1.1", true},
		{"GPTBot/1.0", true},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/120", false},
	}

	for _, tc := range cases {
		got, _ := b.IsBot(tc.ua, "")
		if got != tc.want {
			t.Errorf("IsBot(%q) = %v, want %v", tc.ua, got, tc.want)
		}
	}
}

func TestBotDetector_DatacenterCIDR(t *testing.T) {
	t.Parallel()

	b := enrich.NewBotDetector(discardLogger())
	b.SetDatacenterCIDRs([]netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")})

	if got, reason := b.IsBot("Mozilla/5.0 BrowserLike", "198.51.100.5"); !got || reason != "datacenter_cidr" {
		t.Errorf("expected datacenter_cidr hit; got=%v reason=%q", got, reason)
	}

	if got, _ := b.IsBot("Mozilla/5.0 BrowserLike", "203.0.113.5"); got {
		t.Error("non-datacenter IP should not flag")
	}
}
