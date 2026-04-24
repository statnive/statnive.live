package about_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/about"
)

// The CC-BY-SA-4.0 §3(a)(1) attribution requirement is satisfied only
// if this exact string appears in every required surface. LICENSE-
// third-party.md carries surface #1; this test pins /api/about
// (surface #2). The dashboard footer test (surface #3) lives in
// web/e2e/.
const ip2locationVerbatim = "This site or product includes IP2Location LITE data available from https://lite.ip2location.com."

func TestHandler_VerbatimIP2LocationAttribution(t *testing.T) {
	t.Parallel()

	h := about.Handler(about.BuildInfo{
		Version:   "v0.9.0-test",
		GitSHA:    "abc1234",
		GoVersion: "go1.25.9",
	}, about.DefaultAttributions())

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/about", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var body about.Response
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Version != "v0.9.0-test" || body.GitSHA != "abc1234" {
		t.Errorf("build info = %#v, wanted the injected values", body)
	}

	var found bool

	for _, a := range body.Attributions {
		if a.Name == "IP2Location LITE DB23" {
			found = true

			if a.Text != ip2locationVerbatim {
				t.Errorf("IP2Location attribution text:\n  got:  %q\n  want: %q", a.Text, ip2locationVerbatim)
			}

			if a.License != "CC-BY-SA-4.0" {
				t.Errorf("license = %q, want CC-BY-SA-4.0", a.License)
			}
		}
	}

	if !found {
		t.Fatal("IP2Location attribution missing from /api/about response")
	}
}

func TestHandler_ContentTypeJSON(t *testing.T) {
	t.Parallel()

	h := about.Handler(about.BuildInfo{}, about.DefaultAttributions())

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/about", nil))

	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json*", ct)
	}
}

func TestHandler_SnapshotIsolation(t *testing.T) {
	t.Parallel()

	// Post-construction mutation of the caller's slice must not leak
	// into the serialized response. Pins the shallow-copy guarantee in
	// the handler constructor.
	attrs := about.DefaultAttributions()
	h := about.Handler(about.BuildInfo{}, attrs)

	attrs[0].Text = "tampered"

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/about", nil))

	var body about.Response
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if strings.Contains(body.Attributions[0].Text, "tampered") {
		t.Fatal("caller mutation leaked into handler snapshot")
	}
}
