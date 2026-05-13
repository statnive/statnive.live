package legal

import (
	_ "embed"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/audit"
)

//go:embed templates/privacy_policy_en.md
var privacyPolicyEN []byte

//go:embed templates/privacy_policy_de.md
var privacyPolicyDE []byte

// PrivacyPolicyHandler serves GET /legal/privacy-policy/{lang}. lang
// is a chi URL param validated against {"en","de"} — unknown values
// return 404. The two embedded templates ship as text/markdown so a
// reverse proxy or a Markdown viewer can render them; the operator's
// frontend renders them inline on /privacy.
func PrivacyPolicyHandler(auditLog *audit.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := chi.URLParam(r, "lang")

		var body []byte
		switch lang {
		case "en":
			body = privacyPolicyEN
		case "de":
			body = privacyPolicyDE
		default:
			http.NotFound(w, r)

			return
		}

		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Language", lang)

		emitView(r.Context(), auditLog, audit.EventPrivacyPolicyViewed, lang)

		_, _ = w.Write(body)
	})
}
