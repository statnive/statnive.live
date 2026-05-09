package landing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testCfg is the canonical Config used by every Handler() test —
// non-empty Version so we can assert template injection without
// depending on the empty-string "dev" fallback.
var testCfg = Config{Version: "v0.0.6-test"}

func TestHandler_GETReturnsLanding(t *testing.T) {
	t.Parallel()

	if len(indexHTML) == 0 {
		t.Fatal("embedded index.html is empty")
	}

	rr := httptest.NewRecorder()
	Handler(testCfg).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}

	if got := rr.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type: got %q", got)
	}

	body := rr.Body.String()
	for _, want := range []string{
		`data-form="GBS1Qd"`,
		`https://assets.mailerlite.com/js/universal.js`,
		`ml('account'`,
		`'2315266'`,
		`/app/`,
		testCfg.Version,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}

	if strings.Contains(body, versionPlaceholder) {
		t.Errorf("body still contains the placeholder %q — version not injected", versionPlaceholder)
	}
}

func TestHandler_VersionDefaultsToDevWhenEmpty(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	Handler(Config{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rr.Body.String()
	if !strings.Contains(body, "dev · Netcup DE") {
		t.Errorf("body missing dev fallback in meta strip")
	}
}

func TestHandler_HEADReturns200(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	Handler(testCfg).ServeHTTP(rr, httptest.NewRequest(http.MethodHead, "/", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	// httptest.ResponseRecorder does not strip HEAD bodies the way the
	// real net/http server does, so we don't assert body length here —
	// stdlib enforces it at the transport layer in production.
}

func TestHandler_DisallowedMethodsReturn405(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			Handler(testCfg).ServeHTTP(rr, httptest.NewRequest(method, "/", nil))

			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("status: got %d, want 405", rr.Code)
			}

			if got := rr.Header().Get("Allow"); got != "GET, HEAD" {
				t.Errorf("Allow: got %q, want %q", got, "GET, HEAD")
			}
		})
	}
}

func TestHandler_SetsSecurityHeaders(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	Handler(testCfg).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := rr.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'self'",
		"https://groot.mailerlite.com",
		"https://assets.mailerlite.com",
		"https://assets.mlcdn.com",
		"frame-ancestors 'none'",
		"form-action https://assets.mailerlite.com",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q\nfull: %s", want, csp)
		}
	}

	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options: got %q", got)
	}

	if got := rr.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy: got %q", got)
	}

	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=300, must-revalidate" {
		t.Errorf("Cache-Control: got %q", got)
	}
}

func TestFaviconHandler_GETReturnsSVG(t *testing.T) {
	t.Parallel()

	if len(faviconSVG) == 0 {
		t.Fatal("embedded favicon.svg is empty")
	}

	rr := httptest.NewRecorder()
	FaviconHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}

	if got := rr.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Errorf("Content-Type: got %q, want image/svg+xml", got)
	}

	if !strings.Contains(rr.Body.String(), "<svg") {
		t.Errorf("body missing <svg root: %q", rr.Body.String())
	}
}

func TestFaviconHandler_DisallowedMethodsReturn405(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			rr := httptest.NewRecorder()
			FaviconHandler().ServeHTTP(rr, httptest.NewRequest(method, "/favicon.ico", nil))

			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("status: got %d, want 405", rr.Code)
			}
		})
	}
}
