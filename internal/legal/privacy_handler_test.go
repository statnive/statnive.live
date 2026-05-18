package legal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/middleware"
)

const testSecretLegal = "test-secret-32-bytes-of-padding!!" //nolint:gosec // hardcoded TEST secret, not a credential.

// validatorAccept matches any hostname; used by tests that don't
// exercise the unknown-site rejection path.
func validatorAccept(_ string) bool { return true }

func TestPrivacyHandler_RendersPageAndIssuesCSRFCookie(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	rec := httptest.NewRecorder()

	PrivacyHandler(nil, []byte(testSecretLegal), validatorAccept).ServeHTTP(rec, req)

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

	if !strings.Contains(body, `"`+csrf.Value+`"`) {
		t.Errorf("CSRF meta tag value does not match cookie value")
	}
}

func TestPrivacyHandler_SecurityHeaders(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	rec := httptest.NewRecorder()

	PrivacyHandler(nil, []byte(testSecretLegal), validatorAccept).ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}

	if got := rec.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q, want strict-origin-when-cross-origin", got)
	}

	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY (clickjacking defence)", got)
	}

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP missing frame-ancestors 'none': %q", csp)
	}

	if !strings.Contains(csp, "form-action 'none'") {
		t.Errorf("CSP missing form-action 'none': %q", csp)
	}

	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("Content-Type should be text/html, got %q", rec.Header().Get("Content-Type"))
	}
}

func TestPrivacyHandler_SiteParam_Valid(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/privacy?site=televika.com", nil)
	rec := httptest.NewRecorder()

	called := ""
	validator := func(h string) bool {
		called = h

		return true
	}

	PrivacyHandler(nil, []byte(testSecretLegal), validator).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if called != "televika.com" {
		t.Errorf("validator received %q, want televika.com", called)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `<meta name="statnive-site" content="televika.com">`) {
		t.Errorf("body missing statnive-site meta with operator hostname")
	}

	if !strings.Contains(body, "Accept analytics") {
		t.Errorf("Accept button absent when site param is valid")
	}
}

func TestPrivacyHandler_SiteParam_Unknown(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/privacy?site=evil.example", nil)
	rec := httptest.NewRecorder()

	PrivacyHandler(nil, []byte(testSecretLegal), func(_ string) bool { return false }).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown ?site= should be 400, got %d", rec.Code)
	}
}

func TestPrivacyHandler_SiteParam_XSSAttempt(t *testing.T) {
	t.Parallel()

	// Hostname validator rejects anything with non-DNS chars; this
	// must 400 before reaching the html/template auto-escape layer.
	req := httptest.NewRequest(http.MethodGet, "/privacy?site=<script>alert(1)</script>", nil)
	rec := httptest.NewRecorder()

	PrivacyHandler(nil, []byte(testSecretLegal), validatorAccept).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("XSS attempt in ?site= should be 400, got %d", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "<script>alert(1)</script>") {
		t.Errorf("unescaped script tag in body — auto-escape failed")
	}
}

func TestPrivacyHandler_SiteParam_MissingOK(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	rec := httptest.NewRecorder()

	PrivacyHandler(nil, []byte(testSecretLegal), validatorAccept).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; missing ?site= must still render", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `<meta name="statnive-site" content="">`) {
		t.Errorf("empty Site context should render empty meta tag")
	}

	if strings.Contains(body, "Accept analytics") {
		t.Errorf("Accept button must NOT render without ?site=")
	}
}
