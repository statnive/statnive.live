// Package landing serves the public coming-soon page at GET / on the
// statnive.live SaaS deployment, plus a small SVG favicon at
// /favicon.ico.
//
// Air-gap carve-out: the Iranian-DC binary does not register either
// route (no public marketing surface — Architecture C).
//
// Sub-processor disclosure: MailerLite is registered in
// docs/compliance/subprocessor-register.md and docs/dpa-draft.md; both
// must be merged before this handler is publicly reachable.
package landing

import (
	"bytes"
	_ "embed"
	"fmt"
	"net/http"
)

//go:embed index.html
var indexHTML []byte

//go:embed favicon.svg
var faviconSVG []byte

// versionPlaceholder is the literal string in index.html that
// Handler() rewrites with cfg.Version at construction time. Mirrors
// the SPA bearer-token-injection pattern in internal/dashboard/spa.
const versionPlaceholder = "__STATNIVE_VERSION__"

// CSP is intentionally looser than the SPA's `'self'`-only policy in
// internal/dashboard/spa: MailerLite's Universal embed (universal.js)
// fans out to multiple MailerLite hosts at runtime, including loading
// jQuery from the MailerLite CDN and a font stylesheet from
// fonts.mailerlite.com. The host list was confirmed via a local
// browser preview against the verbatim Universal snippet — see PR #99
// description. Nonce-rewriting per request would risk breaking the
// form whenever MailerLite ships a new template, so 'unsafe-inline'
// is preferred over a brittle nonce.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' https://groot.mailerlite.com https://assets.mailerlite.com https://assets.mlcdn.com https://static.mailerlite.com; " +
	"style-src 'self' 'unsafe-inline' https://assets.mlcdn.com https://assets.mailerlite.com https://fonts.mailerlite.com; " +
	"img-src 'self' data: https://assets.mlcdn.com; " +
	"font-src 'self' https://assets.mlcdn.com https://fonts.mailerlite.com; " +
	"connect-src 'self' https://assets.mailerlite.com https://groot.mailerlite.com; " +
	"form-action https://assets.mailerlite.com; " +
	"base-uri 'self'; " +
	"frame-ancestors 'none'"

// Config wires build metadata into the rendered page. Empty Version
// falls back to "dev" so the meta strip never reads a placeholder.
type Config struct {
	Version string
}

// Handler returns an http.Handler for the public coming-soon page.
// Mount with router.Method(http.MethodGet, "/", landing.Handler(cfg))
// and router.Method(http.MethodHead, "/", landing.Handler(cfg)); chi
// v5's Method registration is exact-path so /anything else falls
// through.
//
// The version placeholder in index.html is replaced once at
// construction; the rendered bytes are then served on every request
// with no per-request allocation. A boot-time count check guarantees
// the placeholder is present exactly once — a copy-paste that
// duplicates or deletes it would fail loudly here instead of leaking
// the literal `__STATNIVE_VERSION__` to a visitor.
func Handler(cfg Config) http.Handler {
	if got := bytes.Count(indexHTML, []byte(versionPlaceholder)); got != 1 {
		panic(fmt.Sprintf("landing: index.html must contain %q exactly once, got %d", versionPlaceholder, got))
	}

	version := cfg.Version
	if version == "" {
		version = "dev"
	}

	rendered := bytes.Replace(indexHTML, []byte(versionPlaceholder), []byte(version), 1)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		// 5-minute cache: short enough to iterate copy quickly, long
		// enough to absorb a launch-tweet hug-of-death.
		h.Set("Cache-Control", "public, max-age=300, must-revalidate")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "interest-cohort=(), camera=(), microphone=(), geolocation=()")

		_, _ = w.Write(rendered)
	})
}

// FaviconHandler returns the inline SVG favicon. Browsers fetch
// /favicon.ico unconditionally; serving anything cheap at this path
// avoids the 404 noise in dev tools and saves an upstream round-trip
// on the cached 5-minute landing render.
//
// Color-sync contract: favicon.svg uses literal hex colours
// (#c8401f vermilion, #f1ede1 bone) that mirror index.html's CSS
// custom properties --vermilion / --bone. SVG can't reach the page's
// CSS vars, so a re-theme has to update both files in lockstep.
func FaviconHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		h := w.Header()
		h.Set("Content-Type", "image/svg+xml")
		h.Set("Cache-Control", "public, max-age=86400, immutable")

		_, _ = w.Write(faviconSVG)
	})
}
