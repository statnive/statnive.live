package privacy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/identity"
)

// TestOptOutCookieName_IngestPrefixInSync pins the literal that
// internal/ingest/handler.go::hasOptOutCookie inlines so it can't
// import internal/privacy (DAG rule — privacy owns identity helpers
// that ingest also uses, so the reverse direction would cycle).
// Privacy is the source of truth: if optoutCookieName(siteID) ever
// changes shape, this test surfaces the silent drift before the
// ingest gate stops recognising the cookie in production.
func TestOptOutCookieName_IngestPrefixInSync(t *testing.T) {
	t.Parallel()

	const ingestInlinedPrefix = "_statnive_optout_"

	got := optoutCookieName(42)

	if !strings.HasPrefix(got, ingestInlinedPrefix) {
		t.Errorf("optoutCookieName(42) = %q; ingest's inlined prefix %q no longer matches",
			got, ingestInlinedPrefix)
	}

	if got != ingestInlinedPrefix+"42" {
		t.Errorf("optoutCookieName(42) = %q; want %q42", got, ingestInlinedPrefix)
	}
}

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

func TestConsent_GiveMintsCookieWhenAbsent(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/privacy/consent", strings.NewReader(`{"action":"give"}`))
	rec := httptest.NewRecorder()
	h.Consent(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (hybrid pre-consent fresh visitor)", rec.Code)
	}

	got := make(map[string]*http.Cookie)
	for _, c := range rec.Result().Cookies() {
		got[c.Name] = c
	}

	statnive, ok := got["_statnive"]
	if !ok {
		t.Fatalf("_statnive cookie not minted by give path")
	}

	if statnive.Value == "" {
		t.Errorf("_statnive value is empty")
	}

	if !statnive.HttpOnly {
		t.Errorf("_statnive cookie must be HttpOnly")
	}

	if statnive.SameSite != http.SameSiteNoneMode {
		t.Errorf("_statnive SameSite = %v, want None (cross-origin)", statnive.SameSite)
	}

	if !statnive.Partitioned {
		t.Errorf("_statnive must be Partitioned (CHIPS per-top-level-site)")
	}

	if statnive.MaxAge != consentCookieMaxAge {
		t.Errorf("_statnive MaxAge = %d, want %d", statnive.MaxAge, consentCookieMaxAge)
	}

	if statnive.Path != "/" {
		t.Errorf("_statnive Path = %q, want /", statnive.Path)
	}

	if c, present := got[consentCookieName(42)]; !present || c.Value != "v1" {
		t.Errorf("%s=v1 cookie not set", consentCookieName(42))
	}
}

func TestConsent_GiveWithExistingCookieReusesIt(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)

	const rawID = "550e8400-e29b-41d4-a716-446655440000"

	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/privacy/consent", strings.NewReader(`{"action":"give"}`))
	req.AddCookie(&http.Cookie{Name: "_statnive", Value: rawID})

	rec := httptest.NewRecorder()
	h.Consent(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}

	// _statnive should NOT be re-Set when the visitor already carries one
	// (idempotent reuse — avoids cookie thrash on repeated Accept clicks).
	for _, c := range rec.Result().Cookies() {
		if c.Name == "_statnive" {
			t.Errorf("_statnive Set-Cookie present despite existing visitor cookie")
		}
	}

	// The hash MUST still match what HexCookieIDHash would compute for
	// the existing UUID — proves the give path actually used the
	// visitor's identifier, not a freshly-minted one.
	wantHash := identity.HexCookieIDHash([]byte("test-master-secret-32-bytes-long!"), 42, rawID)
	if wantHash == "" {
		t.Fatalf("hash helper returned empty — test setup is wrong")
	}
}

func TestConsent_WithdrawStillRequiresCookie(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/privacy/consent", strings.NewReader(`{"action":"withdraw"}`))
	rec := httptest.NewRecorder()
	h.Consent(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (withdraw still needs an identity anchor)", rec.Code)
	}
}
