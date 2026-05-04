// Package landing serves the public coming-soon page at GET / on the
// statnive.live SaaS deployment.
//
// Air-gap carve-out: the Iranian-DC binary does not register this route
// (no public marketing surface — Architecture C).
//
// Sub-processor disclosure: MailerLite is registered in
// docs/compliance/subprocessor-register.md and docs/dpa-draft.md; both
// must be merged before this handler is publicly reachable.
package landing

import (
	_ "embed"
	"net/http"
)

//go:embed index.html
var indexHTML []byte

// CSP is intentionally looser than the SPA's `'self'`-only policy in
// internal/dashboard/spa: MailerLite's embed loads scripts + fonts from
// three of its hosts and emits an inline success callback. Nonce-rewriting
// per request would risk breaking the form whenever MailerLite ships a new
// template, so 'unsafe-inline' is preferred over a brittle nonce.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' https://groot.mailerlite.com https://assets.mailerlite.com; " +
	"style-src 'self' 'unsafe-inline' https://assets.mlcdn.com; " +
	"img-src 'self' data: https://assets.mlcdn.com; " +
	"font-src 'self' https://assets.mlcdn.com; " +
	"connect-src 'self' https://assets.mailerlite.com https://groot.mailerlite.com; " +
	"form-action https://assets.mailerlite.com; " +
	"base-uri 'self'; " +
	"frame-ancestors 'none'"

// Handler returns an http.Handler for the public coming-soon page.
// Mount with router.Method(http.MethodGet, "/", landing.Handler()) and
// router.Method(http.MethodHead, "/", landing.Handler()); chi v5's
// Method registration is exact-path so /anything else falls through.
func Handler() http.Handler {
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

		_, _ = w.Write(indexHTML)
	})
}
