package privacy

// Stage-4 dual-read cutoff tests. Pin the behaviour that:
//   - per-site cookie (_statnive_consent_<id>) is the canonical
//     consent signal.
//   - legacy single _statnive_consent cookie still counts BEFORE
//     legacyCookieCutoff, fires the metric observer on hit.
//   - after the cutoff, legacy cookies are ignored.
//   - one operator's per-site cookie does NOT grant consent on
//     another operator's site (multi-tenancy isolation).

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/sites"
)

func TestHasValidConsent_PerSiteCookieWins(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: consentCookieName(4), Value: "v1"})

	if !hasValidConsent(req, 4) {
		t.Error("per-site cookie must grant consent")
	}
}

func TestHasValidConsent_PerSiteIsolated(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	// Consent given on site 4 only.
	req.AddCookie(&http.Cookie{Name: consentCookieName(4), Value: "v1"})

	if hasValidConsent(req, 5) {
		t.Error("consent for site 4 must NOT leak to site 5")
	}
}

//nolint:paralleltest // mutates package-level legacyCookieCutoff; must run serially with the After-cutoff sibling.
func TestHasValidConsent_LegacyCookieBeforeCutoff(t *testing.T) {
	// Push cutoff far in the future so the legacy cookie counts.
	prev := legacyCookieCutoff.Load()
	defer legacyCookieCutoff.Store(prev)

	future := time.Now().Add(24 * time.Hour)
	legacyCookieCutoff.Store(&future)

	var hits atomic.Int64

	SetLegacyCookieReadObserver(func(_ uint32) { hits.Add(1) })

	defer SetLegacyCookieReadObserver(nil)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: LegacyConsentCookieName, Value: "v1"})

	if !hasValidConsent(req, 4) {
		t.Error("legacy cookie must grant consent before cutoff")
	}

	if hits.Load() != 1 {
		t.Errorf("legacy-cookie observer fired %d times, want 1", hits.Load())
	}
}

//nolint:paralleltest // mutates package-level legacyCookieCutoff; pairs with Before-cutoff sibling.
func TestHasValidConsent_LegacyCookieAfterCutoff(t *testing.T) {
	prev := legacyCookieCutoff.Load()
	defer legacyCookieCutoff.Store(prev)

	past := time.Now().Add(-24 * time.Hour)
	legacyCookieCutoff.Store(&past)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: LegacyConsentCookieName, Value: "v1"})

	if hasValidConsent(req, 4) {
		t.Error("legacy cookie must NOT grant consent after cutoff")
	}
}

func TestPolicyToMode_HybridUsesPerSiteCookie(t *testing.T) {
	t.Parallel()

	const id uint32 = 7

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: consentCookieName(id), Value: "v1"})

	got := PolicyToMode(req, id, sites.SitePolicy{ConsentMode: sites.ConsentModeHybrid})
	if got != ModeHybridPostConsent {
		t.Errorf("hybrid + per-site cookie → got %v, want ModeHybridPostConsent", got)
	}

	// Same cookie under a different siteID must NOT promote.
	got = PolicyToMode(req, id+1, sites.SitePolicy{ConsentMode: sites.ConsentModeHybrid})
	if got != ModeHybridPreConsent {
		t.Errorf("hybrid + different-site cookie → got %v, want ModeHybridPreConsent (isolation)", got)
	}
}
