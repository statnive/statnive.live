package legal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// makeRouted wraps the handler in a chi router so chi.URLParam("lang")
// resolves correctly without manually faking the chi.RouteCtxKey.
func makeRouted() *chi.Mux {
	r := chi.NewRouter()
	r.Method(http.MethodGet, "/legal/privacy-policy/{lang}", PrivacyPolicyHandler(nil))

	return r
}

func TestPrivacyPolicyHandler_ServesPerLang(t *testing.T) {
	t.Parallel()

	cases := []struct {
		lang   string
		marker string
	}{
		{"en", "Privacy notice"},
		{"de", "Datenschutzhinweis"},
	}
	for _, c := range cases {
		t.Run(c.lang, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/legal/privacy-policy/"+c.lang, nil)
			rec := httptest.NewRecorder()
			makeRouted().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}

			if got := rec.Header().Get("Content-Language"); got != c.lang {
				t.Errorf("Content-Language = %q, want %s", got, c.lang)
			}

			if !strings.Contains(rec.Body.String(), c.marker) {
				t.Errorf("body missing marker %q", c.marker)
			}
		})
	}
}

func TestPrivacyPolicyHandler_UnknownLang404(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/legal/privacy-policy/fr", nil)
	rec := httptest.NewRecorder()
	makeRouted().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Compile-time guard so this file compiles regardless of context import
// usage in future iterations.
var _ = context.TODO
