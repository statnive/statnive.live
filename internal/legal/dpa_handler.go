package legal

import (
	_ "embed"
	"net/http"

	"github.com/statnive/statnive.live/internal/audit"
)

//go:embed templates/dpa.md
var dpaTemplate []byte

// DPAHandler returns the http.Handler for GET /legal/dpa. The body is
// the customer-facing Data Processing Agreement template, embedded at
// build time from docs/dpa-draft.md. English-only — translations are a
// Phase 11a follow-up. Audit is optional — nil-safe for tests.
func DPAHandler(auditLog *audit.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Language", "en")

		emitView(r.Context(), auditLog, audit.EventDPAViewed, "en")

		_, _ = w.Write(dpaTemplate)
	})
}
