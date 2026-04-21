package spa

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Security-header assertions: every /app/ response MUST carry CSP,
// nosniff, and Referrer-Policy. Enforces the air-gap invariant in the
// browser even if a future PR slips a CDN URL past code review.

func newHandlerForTest(t *testing.T, token string) http.Handler {
	t.Helper()

	h, err := Handler(Config{BearerToken: token})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	return h
}

func TestHandler_EmitsSecurityHeaders(t *testing.T) {
	t.Parallel()

	h := newHandlerForTest(t, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: status %d, want 200", rec.Code)
	}

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP missing default-src 'self'; got %q", csp)
	}

	if !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("CSP missing connect-src 'self'; got %q", csp)
	}

	if !strings.Contains(csp, "font-src 'self'") {
		t.Errorf("CSP missing font-src 'self'; got %q", csp)
	}

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}

	if got := rec.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q, want strict-origin-when-cross-origin", got)
	}
}

func TestHandler_IndexContainsRootDiv(t *testing.T) {
	t.Parallel()

	h := newHandlerForTest(t, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `<div id="statnive-app">`) {
		t.Errorf("index.html missing SPA root div; body: %q", body)
	}
}

func TestHandler_BearerTokenInjection(t *testing.T) {
	t.Parallel()

	h := newHandlerForTest(t, "tok-xyz-123")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `content="tok-xyz-123"`) {
		t.Errorf("bearer token not injected; body: %q", body)
	}

	// Placeholder must be replaced.
	if strings.Contains(body, bearerPlaceholder) {
		t.Errorf("bearer placeholder still present in body")
	}
}

func TestHandler_EmptyTokenClearsPlaceholder(t *testing.T) {
	t.Parallel()

	h := newHandlerForTest(t, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, bearerPlaceholder) {
		t.Errorf("empty-token case should still replace placeholder; got %q", body)
	}

	if !strings.Contains(body, `content=""`) {
		t.Errorf("expected empty content attr for dev-mode token; body: %q", body)
	}
}

func TestHandler_Missing404AssetStillEmitsSecurityHeaders(t *testing.T) {
	t.Parallel()

	h := newHandlerForTest(t, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/does-not-exist.js", nil)
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("CSP missing on 404 response")
	}
}

func TestHandler_RejectsNonexistentWithoutEatingErrors(t *testing.T) {
	t.Parallel()

	// This test is a guard against a future regression where a handler
	// change silently 200s every request (e.g. fallback-to-index on all
	// 404s). Paths under /assets/ with no real asset MUST NOT fall
	// through to index.html — they MUST 404 (or the asset's own 404).
	h := newHandlerForTest(t, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/never-existed-in-any-build.js", nil)
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		body, _ := io.ReadAll(rec.Body)
		if strings.Contains(string(body), "statnive-app") {
			t.Errorf("missing asset fell through to index.html (would shadow real 404s)")
		}
	}
}
