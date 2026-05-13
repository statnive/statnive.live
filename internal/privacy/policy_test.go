package privacy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/statnive/statnive.live/internal/sites"
)

// TestPolicyToMode_EmptyModeStaysCurrent pins the migrated-row safety
// net: a SitePolicy with ConsentMode == "" (a row that pre-dates
// migration 013 or hit the backfill default-empty path) falls back to
// ModeCurrent. The 3 live operators rely on this so a partially-
// applied migration can't silently widen their data surface.
func TestPolicyToMode_EmptyModeStaysCurrent(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	if got := PolicyToMode(req, sites.SitePolicy{}); got != ModeCurrent {
		t.Errorf("PolicyToMode(empty) = %v, want ModeCurrent", got)
	}
}

func TestPolicyToMode_AllSixModes(t *testing.T) {
	t.Parallel()

	plainReq := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	consented := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	consented.AddCookie(&http.Cookie{Name: "_statnive_consent", Value: "v1"})

	cases := []struct {
		name string
		mode string
		req  *http.Request
		want Mode
	}{
		{"permissive", "permissive", plainReq, ModePermissive},
		{"consent-free", "consent-free", plainReq, ModeConsentFree},
		{"consent-required without consent", "consent-required", plainReq, ModeConsentRequired},
		{"consent-required with consent", "consent-required", consented, ModePermissive},
		{"hybrid pre-consent", "hybrid", plainReq, ModeHybridPreConsent},
		{"hybrid post-consent (cookie)", "hybrid", consented, ModeHybridPostConsent},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := PolicyToMode(c.req, sites.SitePolicy{ConsentMode: c.mode})
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestPolicyToMode_HybridConsentViaHeader(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	req.Header.Set("X-Statnive-Consent", "given")

	got := PolicyToMode(req, sites.SitePolicy{ConsentMode: "hybrid"})
	if got != ModeHybridPostConsent {
		t.Errorf("got %v, want ModeHybridPostConsent (header override)", got)
	}
}

func TestMode_BehaviourPredicates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		m                 Mode
		anonymous, allows bool
		allowlist         bool
	}{
		{ModeCurrent, false, true, false},
		{ModePermissive, false, true, false},
		{ModeConsentFree, true, false, true},
		{ModeConsentRequired, false, false, false},
		{ModeHybridPreConsent, true, false, true},
		{ModeHybridPostConsent, false, true, false},
	}
	for _, c := range cases {
		if got := c.m.AnonymousCount(); got != c.anonymous {
			t.Errorf("%v.AnonymousCount() = %v, want %v", c.m, got, c.anonymous)
		}

		if got := c.m.AllowsIdentifier(); got != c.allows {
			t.Errorf("%v.AllowsIdentifier() = %v, want %v", c.m, got, c.allows)
		}

		if got := c.m.EnforcesEventAllowlist(); got != c.allowlist {
			t.Errorf("%v.EnforcesEventAllowlist() = %v, want %v", c.m, got, c.allowlist)
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
