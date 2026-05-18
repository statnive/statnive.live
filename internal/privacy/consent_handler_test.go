package privacy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func consentRequestFor(t *testing.T, body string) *http.Request {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "https://example.com/api/privacy/consent", strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: "_statnive", Value: "550e8400-e29b-41d4-a716-446655440000"})

	return r
}

func TestConsent_GiveSetsCookie(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)

	rec := httptest.NewRecorder()
	h.Consent(rec, consentRequestFor(t, `{"action":"give"}`))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}

	var consent *http.Cookie

	wantName := consentCookieName(42) // matches fakeSitesResolver{siteID: 42}

	for _, c := range rec.Result().Cookies() {
		if c.Name == wantName {
			consent = c
			break
		}
	}

	if consent == nil {
		t.Fatalf("%s cookie not set", wantName)
		return // unreachable; staticcheck SA5011 doesn't see t.Fatalf as terminal
	}

	if consent.Value != "v1" {
		t.Errorf("cookie value = %q, want v1", consent.Value)
	}

	if !consent.HttpOnly {
		t.Errorf("consent cookie should be HttpOnly")
	}

	if consent.SameSite != http.SameSiteNoneMode {
		t.Errorf("SameSite = %v, want None (Stage-4 cross-origin)", consent.SameSite)
	}

	if !consent.Partitioned {
		t.Errorf("Partitioned must be true (CHIPS per-top-level-site isolation)")
	}
}

func TestConsent_WithdrawClearsAndSuppresses(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)

	rec := httptest.NewRecorder()
	h.Consent(rec, consentRequestFor(t, `{"action":"withdraw"}`))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}

	got := make(map[string]*http.Cookie)
	for _, c := range rec.Result().Cookies() {
		got[c.Name] = c
	}

	perSiteConsent := consentCookieName(42)
	perSiteOptout := optoutCookieName(42)

	if c, ok := got[perSiteConsent]; !ok {
		t.Errorf("%s expiry not set in response", perSiteConsent)
	} else if c.MaxAge >= 0 {
		t.Errorf("%s MaxAge = %d, want -1", perSiteConsent, c.MaxAge)
	}

	// Legacy cookie is defanged in the same response (dual-read window).
	if c, ok := got["_statnive_consent"]; !ok {
		t.Errorf("legacy _statnive_consent expiry not set in response")
	} else if c.MaxAge >= 0 {
		t.Errorf("legacy _statnive_consent MaxAge = %d, want -1", c.MaxAge)
	}

	if c, ok := got["_statnive"]; !ok {
		t.Errorf("_statnive expiry not set in response")
	} else if c.MaxAge >= 0 {
		t.Errorf("_statnive MaxAge = %d, want -1", c.MaxAge)
	}

	if c, ok := got[perSiteOptout]; !ok {
		t.Errorf("%s cookie not set on withdraw", perSiteOptout)
	} else if c.Value != "v1" {
		t.Errorf("%s value = %q, want v1", perSiteOptout, c.Value)
	}

	if h.cfg.Suppression.Len() != 1 {
		t.Errorf("withdraw should suppress visitor; Len = %d", h.cfg.Suppression.Len())
	}
}

func TestConsent_UnknownActionRejected(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)

	rec := httptest.NewRecorder()
	h.Consent(rec, consentRequestFor(t, `{"action":"maybe"}`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestConsent_BadJSONRejected(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)

	rec := httptest.NewRecorder()
	h.Consent(rec, consentRequestFor(t, `{not json`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestConsent_NoStatniveCookieReturns401(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/privacy/consent", strings.NewReader(`{"action":"give"}`))
	rec := httptest.NewRecorder()
	h.Consent(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
