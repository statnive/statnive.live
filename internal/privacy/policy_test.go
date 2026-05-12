package privacy

import (
	"net/http/httptest"
	"testing"

	"github.com/statnive/statnive.live/internal/sites"
)

// TestPolicyToMode_StubReturnsCurrent pins the Stage 2 contract: the
// helper returns ModeCurrent for every input so handler bodies behave
// identically to pre-Stage-2. Stage 3 replaces the function body and
// flips this test to TestPolicyToMode_AllSixModes.
func TestPolicyToMode_StubReturnsCurrent(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("GET", "/privacy", nil)

	cases := []sites.SitePolicy{
		{RespectDNT: false, RespectGPC: false, TrackBots: true},
		{RespectDNT: true, RespectGPC: true, TrackBots: false},
		{},
	}

	for _, p := range cases {
		if got := PolicyToMode(req, p); got != ModeCurrent {
			t.Errorf("PolicyToMode(%+v) = %v, want ModeCurrent", p, got)
		}
	}
}

func TestMode_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		m    Mode
		want string
	}{
		{ModeCurrent, "current"},
		{ModeConsentFree, "consent-free"},
		{ModeConsentRequired, "consent-required"},
		{ModePermissive, "permissive"},
		{ModeHybridPreConsent, "hybrid-pre-consent"},
		{ModeHybridPostConsent, "hybrid-post-consent"},
		{Mode(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.m.String(); got != c.want {
			t.Errorf("Mode(%d).String() = %q, want %q", c.m, got, c.want)
		}
	}
}
