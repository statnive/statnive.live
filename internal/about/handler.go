// Package about answers GET /api/about — operator-facing deployment
// metadata + third-party attribution. The endpoint is unauthenticated:
// license attribution surfaces (IP2Location LITE CC-BY-SA) must be
// visible to anyone, and the version / build-SHA are non-sensitive.
//
// Three-surface attribution (CLAUDE.md License Rules carve-out):
//
//  1. LICENSE-third-party.md  (ships in the bundle; source of truth)
//  2. GET /api/about          (this package)
//  3. Dashboard footer        (web/src/components/Footer.tsx)
//
// Enforced at CI time by the geoip-pipeline-review Semgrep rule
// `geoip-attribution-string-present`. Edits here must keep the
// CC-BY-SA verbatim string byte-identical to what LICENSE-third-party
// ships.
package about

import (
	"encoding/json"
	"net/http"
)

// Response is the GET /api/about JSON envelope. Stable shape — the
// Attributions array grows with each release as new third-party assets
// are bundled in; consumers (Phase 6-polish-5 Notice UI, a future
// /about page, external audits) match on the Name field.
type Response struct {
	Version      string        `json:"version"`
	GitSHA       string        `json:"git_sha"`
	GoVersion    string        `json:"go_version"`
	Attributions []Attribution `json:"attributions"`
}

// Attribution describes one third-party asset that ships with the
// binary or is an optional operator-installed artifact (LITE GeoIP
// BIN). Text is the verbatim attribution string required under the
// listed license.
type Attribution struct {
	Name    string `json:"name"`
	License string `json:"license"`
	URL     string `json:"url"`
	Text    string `json:"text"`
}

// BuildInfo is the subset of build-time metadata the handler needs.
// Populated in main.go from the embedded VERSION file + runtime.Version.
type BuildInfo struct {
	Version   string
	GitSHA    string
	GoVersion string
}

// DefaultAttributions ships the current list. The IP2Location LITE
// entry is unconditional — operators who don't drop the BIN get the
// no-op enricher, but the attribution stays published (the third-party
// file is referenced by the dashboard footer + /about regardless of
// whether the BIN is loaded, so the string must always be present).
func DefaultAttributions() []Attribution {
	return []Attribution{
		{
			Name:    "IP2Location LITE DB23",
			License: "CC-BY-SA-4.0",
			URL:     "https://lite.ip2location.com",
			Text:    "This site or product includes IP2Location LITE data available from https://lite.ip2location.com.",
		},
		{
			Name:    "crawler-user-agents",
			License: "MIT",
			URL:     "https://github.com/monperrus/crawler-user-agents",
			Text:    "Bot-detection patterns from monperrus/crawler-user-agents (MIT).",
		},
		{
			Name:    "lukechampine/blake3",
			License: "CC0-1.0",
			URL:     "https://github.com/lukechampine/blake3",
			Text:    "BLAKE3 hashing via lukechampine/blake3 (CC0-1.0).",
		},
	}
}

// Handler returns the HTTP handler. build is embedded once at startup
// and lives for the process lifetime; attributions is a snapshot, not
// a live view, to avoid per-request allocation.
func Handler(build BuildInfo, attributions []Attribution) http.Handler {
	// Shallow-copy the attributions so a later caller mutating the
	// input slice can't corrupt a request in-flight.
	snap := make([]Attribution, len(attributions))
	copy(snap, attributions)

	body := Response{
		Version:      build.Version,
		GitSHA:       build.GitSHA,
		GoVersion:    build.GoVersion,
		Attributions: snap,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_ = json.NewEncoder(w).Encode(body)
	})
}
