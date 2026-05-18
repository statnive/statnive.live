package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func resolver(allowed map[string]uint32) OriginResolver {
	return func(origin string) (uint32, bool) {
		id, ok := allowed[origin]

		return id, ok
	}
}

func nopNext() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestCORS_EmptyOriginPassesThrough(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/privacy", nil)

	CORS(resolver(nil))(nopNext()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("no-Origin should pass; got %d", rec.Code)
	}

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("ACAO must NOT be written when Origin is absent")
	}

	if rec.Header().Get("Vary") == "" {
		t.Errorf("Vary: Origin must be set on every CORS-aware response")
	}
}

func TestCORS_NullOriginRejected(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/privacy/consent", nil)
	req.Header.Set("Origin", "null")

	CORS(resolver(map[string]uint32{"null": 1}))(nopNext()).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("Origin: null must be 403, got %d", rec.Code)
	}
}

func TestCORS_UnresolvedOriginNoHeaders(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/privacy/consent", nil)
	req.Header.Set("Origin", "https://evil.example")

	CORS(resolver(map[string]uint32{"https://allowed.example": 1}))(nopNext()).ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("ACAO leaked for unresolved Origin")
	}
}

func TestCORS_UnresolvedOriginOptions403(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/privacy/consent", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "POST")

	CORS(resolver(nil))(nopNext()).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("preflight from unknown Origin must 403, got %d", rec.Code)
	}
}

func TestCORS_PreflightSuccess(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/privacy/consent", nil)
	req.Header.Set("Origin", "https://televika.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "x-csrf-token, content-type")

	chained := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chained = true

		w.WriteHeader(http.StatusOK)
	})

	CORS(resolver(map[string]uint32{"https://televika.com": 4}))(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}

	if chained {
		t.Errorf("preflight must NOT chain to next handler")
	}

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://televika.com" {
		t.Errorf("ACAO = %q, want echoed origin", got)
	}

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("ACAC = %q, want true", got)
	}

	if rec.Header().Get("Vary") == "" {
		t.Errorf("Vary: Origin missing")
	}

	if got := rec.Header().Get("Access-Control-Max-Age"); got != corsMaxAge {
		t.Errorf("Max-Age = %q, want %q", got, corsMaxAge)
	}
}

func TestCORS_RealRequestStashesSiteID(t *testing.T) {
	t.Parallel()

	const wantID uint32 = 4

	var seenID uint32

	var seenOK bool

	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenID, seenOK = SiteIDFromOriginContext(r.Context())
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/privacy/consent", nil)
	req.Header.Set("Origin", "https://televika.com")

	CORS(resolver(map[string]uint32{"https://televika.com": wantID}))(next).ServeHTTP(rec, req)

	if !seenOK || seenID != wantID {
		t.Errorf("ctx stash = (%d, %v); want (%d, true)", seenID, seenOK, wantID)
	}
}

// TestCORS_NeverReflectsWithoutAllowlist — Strapi CVE-2025 class.
// Even an Origin that "looks" reasonable (https, valid hostname) must
// not get ACAO unless the allowlist resolver returns true.
func TestCORS_NeverReflectsWithoutAllowlist(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/privacy/consent", nil)
	req.Header.Set("Origin", "https://attacker.example")

	CORS(resolver(nil))(nopNext()).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Origin reflection without allowlist: ACAO = %q (Strapi-class bug)", got)
	}
}

func TestCORS_SecFetchSiteSameOriginMismatch(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/privacy/consent", nil)
	req.Host = "app.statnive.live"
	req.Header.Set("Origin", "https://televika.com")
	req.Header.Set("Sec-Fetch-Site", "same-origin") // lie

	CORS(resolver(map[string]uint32{"https://televika.com": 4}))(nopNext()).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("Sec-Fetch-Site=same-origin with cross-origin Origin must 403, got %d", rec.Code)
	}
}

func TestSiteIDFromOriginContext_NilSafe(t *testing.T) {
	t.Parallel()

	if id, ok := SiteIDFromOriginContext(context.Background()); ok || id != 0 {
		t.Errorf("empty context returned (%d, %v); want (0, false)", id, ok)
	}
}
