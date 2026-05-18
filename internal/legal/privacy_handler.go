package legal

import (
	"bytes"
	_ "embed"
	"html/template"
	"net/http"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/middleware"
)

//go:embed templates/privacy_page.html
var privacyPageSrc string

// privacyPageTemplate is parsed once at init; the only dynamic field
// is the CSRF token rendered into a meta tag so the inline JS can
// echo it as X-CSRF-Token on the opt-out POST.
var privacyPageTemplate = template.Must(template.New("privacy").Parse(privacyPageSrc))

type privacyPageData struct {
	CSRFToken string
}

// PrivacyHandler serves GET /privacy — the visitor-facing disclosure
// page that links to the LIA / DPA / privacy-policy templates and
// exposes the opt-out button. The handler issues a fresh CSRF cookie
// on each load (one hour TTL) and renders the same token into the
// page's meta tag. Stage-4 takes masterSecret because the HMAC-signed
// double-submit token (Johansson defence) needs the same key the
// /api/privacy/* RequireCSRF middleware uses to verify.
func PrivacyHandler(auditLog *audit.Logger, masterSecret []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		token, err := middleware.IssueCSRFToken(w, masterSecret)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		var buf bytes.Buffer
		if execErr := privacyPageTemplate.Execute(&buf, privacyPageData{CSRFToken: token}); execErr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// /privacy is intentionally a non-audited surface — every
		// visitor lands here, so audit-emitting on view would bloat
		// the JSONL sink with low-signal events. Audit fires on the
		// actual rights-exercise endpoints instead.
		_ = auditLog // reserved for v1.1 if we add a sampled view counter

		_, _ = w.Write(buf.Bytes())
	})
}
