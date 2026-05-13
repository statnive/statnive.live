package tracker_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/tracker"
)

// Bundle budgets from CLAUDE.md tracker spec + the
// preact-signals-bundle-budget skill. The skill itself fires on every PR;
// this Go test is the in-process safety net so a bench fails before
// /simplify rather than after.
//
// Budget bumped 700 → 750 gz on PR D (regen after PR-E #59 endpoint-
// derivation chain landed; that PR shipped without regenerating dist,
// so the previous 700 gz reading was stale). Doc-comment header
// expansion (LEARN.md Lesson 24 attribution) is comment-only and
// stripped at minification — minified size was unaffected.
//
// Bumped again 1500 → 2100 / 750 → 1000 in Stage 3: the consent-free
// flow added a GPC client-probe (gated by data-statnive-honour-gpc=1)
// plus statniveLive.acceptConsent / withdrawConsent helpers. ~500 B
// min / ~190 B gz net. Matches the Makefile tracker-size gate.
const (
	maxMinifiedBytes = 2100
	maxGzippedBytes  = 1000
)

func TestHandler_ServesEmbeddedTracker(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tracker.js", nil)

	tracker.Handler().ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("status = %d; want 200", got)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q; want application/javascript; charset=utf-8", got)
	}

	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "public") || !strings.Contains(got, "max-age=3600") {
		t.Errorf("Cache-Control = %q; want public max-age=3600", got)
	}

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q; want nosniff", got)
	}

	body := rec.Body.Bytes()
	if !bytes.Equal(body, tracker.Bytes()) {
		t.Errorf("response body length %d != embedded length %d", len(body), len(tracker.Bytes()))
	}
}

func TestBundleSize_MinifiedWithinBudget(t *testing.T) {
	t.Parallel()

	size := len(tracker.Bytes())
	if size > maxMinifiedBytes {
		t.Fatalf("tracker.js minified = %d B; budget = %d B (CLAUDE.md tracker spec)", size, maxMinifiedBytes)
	}

	t.Logf("minified: %d B / %d B budget", size, maxMinifiedBytes)
}

func TestBundleSize_GzippedWithinBudget(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	gw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := gw.Write(tracker.Bytes()); err != nil {
		t.Fatalf("gzip write: %v", err)
	}

	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	size := buf.Len()
	if size > maxGzippedBytes {
		t.Fatalf("tracker.js gzipped = %d B; budget = %d B (CLAUDE.md tracker spec)", size, maxGzippedBytes)
	}

	t.Logf("gzipped: %d B / %d B budget", size, maxGzippedBytes)
}

// TestNoExternalReferences enforces the air-gap-validator invariant —
// the embedded tracker must not contain any string that would cause a
// browser to dial out to a non-loopback host.
func TestNoExternalReferences(t *testing.T) {
	t.Parallel()

	source := string(tracker.Bytes())

	forbidden := []string{
		"https://cdn.",
		"https://unpkg.com",
		"https://cdnjs.",
		"https://googleapis.com",
		"http://",            // any plaintext URL
		"XMLHttpRequest",     // sendBeacon + fetch keepalive only
		"new XMLHttpRequest", // belt + suspenders
		"localStorage",       // Privacy Rule — no client-side storage
		"sessionStorage",
		"indexedDB",
		"document.cookie", // tracker doesn't read or write cookies
	}

	for _, needle := range forbidden {
		if strings.Contains(source, needle) {
			t.Errorf("forbidden token %q found in tracker bundle", needle)
		}
	}
}

// TestHandlerCompiles is a smoke check that the Bytes / Handler exports
// stay non-empty even after a `go mod vendor` / fresh checkout.
func TestHandlerCompiles(t *testing.T) {
	t.Parallel()

	if len(tracker.Bytes()) == 0 {
		t.Fatal("tracker.Bytes() returned empty — go:embed broken or build never ran")
	}

	if tracker.Handler() == nil {
		t.Fatal("tracker.Handler() returned nil")
	}

	// Ensure the response body is exactly what's embedded — no transcoding.
	rec := httptest.NewRecorder()
	tracker.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tracker.js", nil))
	got, _ := io.ReadAll(rec.Body)

	if !bytes.Equal(got, tracker.Bytes()) {
		t.Errorf("Handler() returned different bytes than Bytes()")
	}
}
