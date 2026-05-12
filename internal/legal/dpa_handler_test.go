package legal

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDPAHandler_Markdown(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/legal/dpa", nil)
	rec := httptest.NewRecorder()
	DPAHandler(nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown…", got)
	}

	if got := rec.Header().Get("Content-Language"); got != "en" {
		t.Errorf("Content-Language = %q, want en", got)
	}

	if rec.Body.Len() == 0 {
		t.Fatalf("empty body")
	}

	// Pin the DPA marker so a stripped-content regression on the embed
	// path fails the test.
	if !strings.Contains(rec.Body.String(), "Data Processing Agreement") {
		t.Errorf("body missing DPA header marker")
	}
}

func TestDPAHandler_NilAuditSafe(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/legal/dpa", nil)
	rec := httptest.NewRecorder()
	DPAHandler(nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (nil audit logger must not panic)", rec.Code)
	}
}

// TestDPATemplate_MatchesCanonicalDoc pins the embedded copy of the DPA
// to docs/dpa-draft.md. go:embed cannot reference paths outside the
// package directory, so the canonical doc is copied into
// internal/legal/templates/dpa.md. This test fails on drift so reviewers
// catch a one-sided edit.
func TestDPATemplate_MatchesCanonicalDoc(t *testing.T) {
	t.Parallel()

	canonical, err := os.ReadFile("../../docs/dpa-draft.md")
	if err != nil {
		t.Fatalf("read canonical DPA: %v", err)
	}

	if !bytes.Equal(canonical, dpaTemplate) {
		t.Fatalf("internal/legal/templates/dpa.md drifted from docs/dpa-draft.md — sync the two files (the canonical doc is docs/dpa-draft.md)")
	}
}
