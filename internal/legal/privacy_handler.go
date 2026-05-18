package legal

import (
	"bytes"
	_ "embed"
	"html/template"
	"net/http"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/middleware"
	"github.com/statnive/statnive.live/internal/sites"
)

//go:embed templates/privacy_page.html
var privacyPageSrc string

// privacyPageTemplate is parsed once at init. Dynamic fields:
//   - CSRFToken (rendered into <meta name="csrf-token">)
//   - Site (the operator hostname, echoed into <meta name="statnive-site">)
//     used by the inline JS as the X-Statnive-Site header on
//     /api/privacy/{consent,opt-out,erase} POSTs.
var privacyPageTemplate = template.Must(template.New("privacy").Parse(privacyPageSrc))

type privacyPageData struct {
	CSRFToken string
	Site      string
}

// SiteValidator is the minimum contract PrivacyHandler needs to vet
// the ?site= query parameter. Production: a closure around
// *sites.Registry.LookupSiteIDByHostname; tests: a fake. Returns
// true when the hostname is registered + enabled.
type SiteValidator func(hostname string) bool

// PrivacyHandler serves GET /privacy — the visitor-facing disclosure
// page that links to the LIA / DPA / privacy-policy templates and
// exposes the Accept / Withdraw / Opt-out buttons.
//
// Stage-4 takes:
//   - masterSecret for the HMAC-signed CSRF cookie (Johansson defence).
//   - validate to vet the ?site= query parameter. Cross-origin visitors
//     reach /privacy via top-level navigation (no Origin header), so
//     the operator hostname must come from the URL. Shape validation
//     via sites.ValidateHostname + registry membership check are
//     defence in depth against ?site=<script> XSS attempts +
//     ?site=evil.example open-redirect-style probes.
//
// Security response headers (Stage-4 plan §C):
//   - X-Frame-Options: DENY (clickjacking)
//   - Content-Security-Policy: frame-ancestors 'none'
//   - Referrer-Policy: strict-origin-when-cross-origin
//   - X-Content-Type-Options: nosniff
func PrivacyHandler(auditLog *audit.Logger, masterSecret []byte, validate SiteValidator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site, ok := resolveSiteParam(r, validate)
		if !ok {
			http.Error(w, "unknown site", http.StatusBadRequest)

			return
		}

		token, err := middleware.IssueCSRFToken(w, masterSecret)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		var buf bytes.Buffer
		if execErr := privacyPageTemplate.Execute(&buf, privacyPageData{CSRFToken: token, Site: site}); execErr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "private, no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; form-action 'none'")

		_ = auditLog // reserved for v1.1 if we add a sampled view counter

		_, _ = w.Write(buf.Bytes())
	})
}

// resolveSiteParam handles the Stage-4 ?site= flow. Returns the
// operator hostname to echo into the template, or ok=false on any
// validation failure (resulting in 400). Empty / missing ?site=
// degrades to "no site context" — the page still renders, just
// without the per-site Accept button, falling back to the universal
// Opt-out flow.
//
// Rules:
//  1. Apply sites.ValidateHostname for shape (alphanumerics + . + -).
//  2. Look up via validate (closure around the registry); rejects
//     unregistered hostnames so the rendered page can't lie about
//     which operator the visitor is acting on.
//  3. Empty param → return ("", true) — page renders without
//     per-site context.
func resolveSiteParam(r *http.Request, validate SiteValidator) (string, bool) {
	raw := r.URL.Query().Get("site")
	if raw == "" {
		return "", true
	}

	if err := sites.ValidateHostname(raw); err != nil {
		return "", false
	}

	if validate != nil && !validate(raw) {
		return "", false
	}

	return raw, true
}
