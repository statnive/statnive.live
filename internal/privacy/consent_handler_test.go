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

	for _, c := range rec.Result().Cookies() {
		if c.Name == "_statnive_consent" {
			consent = c
			break
		}
	}

	if consent == nil {
		t.Fatalf("_statnive_consent cookie not set")
		return // unreachable; staticcheck SA5011 doesn't see t.Fatalf as terminal
	}

	if consent.Value != "v1" {
		t.Errorf("cookie value = %q, want v1", consent.Value)
	}

	if !consent.HttpOnly {
		t.Errorf("consent cookie should be HttpOnly")
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

	if c, ok := got["_statnive_consent"]; !ok {
		t.Errorf("_statnive_consent expiry not set in response")
	} else if c.MaxAge >= 0 {
		t.Errorf("_statnive_consent MaxAge = %d, want -1", c.MaxAge)
	}

	if c, ok := got["_statnive"]; !ok {
		t.Errorf("_statnive expiry not set in response")
	} else if c.MaxAge >= 0 {
		t.Errorf("_statnive MaxAge = %d, want -1", c.MaxAge)
	}

	if c, ok := got["_statnive_optout"]; !ok {
		t.Errorf("_statnive_optout cookie not set on withdraw")
	} else if c.Value != "v1" {
		t.Errorf("_statnive_optout value = %q, want v1", c.Value)
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
