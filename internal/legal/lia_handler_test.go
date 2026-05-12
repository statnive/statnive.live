package legal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLIAHandler_LocaleNegotiation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		acceptLang     string
		queryLang      string
		wantLang       string
		wantBodyMarker string
	}{
		{
			name:           "no signal → fallback en",
			wantLang:       "en",
			wantBodyMarker: "Legitimate Interest Assessment",
		},
		{
			name:           "Accept-Language de → de",
			acceptLang:     "de-DE,de;q=0.9,en;q=0.8",
			wantLang:       "de",
			wantBodyMarker: "Interessenabwägung",
		},
		{
			name:           "query ?lang=de overrides Accept-Language",
			acceptLang:     "en-US,en;q=0.9",
			queryLang:      "de",
			wantLang:       "de",
			wantBodyMarker: "Interessenabwägung",
		},
		{
			name:           "query ?lang=fr falls through to Accept-Language en",
			acceptLang:     "en-US,en;q=0.9",
			queryLang:      "fr",
			wantLang:       "en",
			wantBodyMarker: "Legitimate Interest Assessment",
		},
		{
			name:           "Accept-Language fr → fallback en",
			acceptLang:     "fr-CA,fr;q=0.9",
			wantLang:       "en",
			wantBodyMarker: "Legitimate Interest Assessment",
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/legal/lia", nil)
			if c.acceptLang != "" {
				req.Header.Set("Accept-Language", c.acceptLang)
			}

			if c.queryLang != "" {
				q := req.URL.Query()
				q.Set("lang", c.queryLang)
				req.URL.RawQuery = q.Encode()
			}

			rec := httptest.NewRecorder()
			LIAHandler(nil).ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}

			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/markdown") {
				t.Errorf("Content-Type = %q, want text/markdown…", got)
			}

			if got := rec.Header().Get("Content-Language"); got != c.wantLang {
				t.Errorf("Content-Language = %q, want %q", got, c.wantLang)
			}

			if !strings.Contains(rec.Body.String(), c.wantBodyMarker) {
				t.Errorf("body missing marker %q", c.wantBodyMarker)
			}
		})
	}
}

func TestLIAHandler_NilAuditSafe(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/legal/lia", nil)
	rec := httptest.NewRecorder()
	LIAHandler(nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (nil audit logger must not panic)", rec.Code)
	}
}
