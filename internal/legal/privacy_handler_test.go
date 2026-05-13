package legal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/middleware"
)

func TestPrivacyHandler_RendersPageAndIssuesCSRFCookie(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	rec := httptest.NewRecorder()

	PrivacyHandler(nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `<meta name="csrf-token"`) {
		t.Errorf("missing CSRF meta tag in body")
	}

	if !strings.Contains(body, "/api/privacy/opt-out") {
		t.Errorf("body should reference the opt-out endpoint")
	}

	// CSRF cookie issued.
	var csrf *http.Cookie

	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.CSRFCookieName {
			csrf = c
			break
		}
	}

	if csrf == nil {
		t.Fatalf("CSRF cookie not set")
		return // unreachable; staticcheck SA5011 doesn't see t.Fatalf as terminal
	}

	if !csrf.HttpOnly {
		t.Errorf("CSRF cookie should be HttpOnly")
	}

	// Token meta tag matches cookie value.
	if !strings.Contains(body, `"`+csrf.Value+`"`) {
		t.Errorf("CSRF meta tag value does not match cookie value")
	}
}

func TestPrivacyHandler_SecurityHeaders(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	rec := httptest.NewRecorder()

	PrivacyHandler(nil).ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}

	if got := rec.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q, want strict-origin-when-cross-origin", got)
	}

	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("Content-Type should be text/html, got %q", rec.Header().Get("Content-Type"))
	}
}
