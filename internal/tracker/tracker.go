// Package tracker serves the embedded JS tracker (`tracker/dist/tracker.js`)
// as a first-party asset under GET /tracker.js.
//
// First-party serving (CLAUDE.md Isolation invariant) is the entire point —
// no external CDN, no third-party DNS hop, no SRI gymnastics, ad-blocker
// resistant. The bytes ship inside the binary via `go:embed` so the air-gap
// install bundle is one file.
package tracker

import (
	_ "embed"
	"net/http"
)

//go:embed dist/tracker.js
var trackerJS []byte

// Bytes returns the embedded tracker source. Exposed so the bundle-size
// regression test can inspect the bytes without going through the handler.
func Bytes() []byte { return trackerJS }

// Handler returns an http.Handler that serves the embedded tracker.js.
//
// Cache-Control: 1 hour with must-revalidate. Long enough to absorb the
// thundering herd at peak; short enough that a tracker bug fix reaches
// every browser within an hour without operator action.
//
// X-Content-Type-Options: nosniff prevents MIME sniffing — defense in
// depth so a future bug serving a different blob can't get reinterpreted
// as HTML by a permissive browser.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(trackerJS)
	})
}
