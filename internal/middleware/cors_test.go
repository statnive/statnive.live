package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

// wwwEquivalentResolver mimics the production sites.OriginIndex.Lookup
// www.-toggle fallback. Kept inline here rather than importing the sites
// package so the middleware test stays unit-scoped (no test-time
// coupling between two packages).
func wwwEquivalentResolver(seeded map[string]uint32) OriginResolver {
	const httpsPrefix = "https://"

	const wwwPrefix = "https://www."

	return func(origin string) (uint32, bool) {
		if id, ok := seeded[origin]; ok {
			return id, true
		}

		var alt string

		switch {
		case strings.HasPrefix(origin, wwwPrefix):
			alt = httpsPrefix + strings.TrimPrefix(origin, wwwPrefix)
		case strings.HasPrefix(origin, httpsPrefix):
			alt = wwwPrefix + strings.TrimPrefix(origin, httpsPrefix)
		default:
			return 0, false
		}

		id, ok := seeded[alt]

		return id, ok
	}
}

// TestCORS_WwwBareEquivalence_Preflight covers both directions of the
// www.-toggle resolver fallback. The middleware MUST echo the REQUEST'S
// Origin in ACAO, not the seeded variant — browser CORS spec requires
// byte-match between request Origin and response ACAO.
func TestCORS_WwwBareEquivalence_Preflight(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		requestOrigin string
		seededOrigin  string
	}{
		{
			name:          "bare request resolves via www allowlist entry",
			requestOrigin: "https://televika.com",
			seededOrigin:  "https://www.televika.com",
		},
		{
			name:          "www request resolves via bare allowlist entry",
			requestOrigin: "https://www.televika.com",
			seededOrigin:  "https://televika.com",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodOptions, "/api/event", nil)
			req.Header.Set("Origin", c.requestOrigin)
			req.Header.Set("Access-Control-Request-Method", "POST")

			CORS(wwwEquivalentResolver(map[string]uint32{c.seededOrigin: 4}))(nopNext()).ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("preflight status = %d, want 204", rec.Code)
			}

			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != c.requestOrigin {
				t.Errorf("ACAO = %q, want request-Origin echo %q (browser CORS spec requires byte-match)", got, c.requestOrigin)
			}
		})
	}
}

// TestCORS_WwwBareEquivalence_PostStashesSiteID — real POST (not
// preflight) from the bare origin must still stash the seeded site_id
// into the request context so downstream handlers see it. Locks the
// invariant that www.-fallback works for the POST path too, not just
// preflight.
func TestCORS_WwwBareEquivalence_PostStashesSiteID(t *testing.T) {
	t.Parallel()

	const wantID uint32 = 4

	var seenID uint32

	var seenOK bool

	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenID, seenOK = SiteIDFromOriginContext(r.Context())
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/event", nil)
	req.Header.Set("Origin", "https://televika.com")

	resolver := wwwEquivalentResolver(map[string]uint32{
		"https://www.televika.com": wantID,
	})

	CORS(resolver)(next).ServeHTTP(rec, req)

	if !seenOK || seenID != wantID {
		t.Errorf("ctx stash via www. fallback = (%d, %v); want (%d, true)", seenID, seenOK, wantID)
	}
}

func TestSiteIDFromOriginContext_NilSafe(t *testing.T) {
	t.Parallel()

	if id, ok := SiteIDFromOriginContext(context.Background()); ok || id != 0 {
		t.Errorf("empty context returned (%d, %v); want (0, false)", id, ok)
	}
}
