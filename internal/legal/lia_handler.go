// Package legal serves the operator-facing legal-disclosure pages
// (LIA, DPA, privacy policy) that customers reach without authentication.
// All content is embedded via go:embed — no external CDN, no external
// fetch (Isolation invariant; air-gap-validator skill).
package legal

import (
	"context"
	_ "embed"
	"log/slog"
	"net/http"
	"strings"

	"github.com/statnive/statnive.live/internal/audit"
)

//go:embed templates/lia_en.md
var liaTemplateEN []byte

//go:embed templates/lia_de.md
var liaTemplateDE []byte

// LIAHandler returns the http.Handler for GET /legal/lia. Audit is
// optional — nil-safe for tests.
func LIAHandler(auditLog *audit.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := negotiateLang(r, []string{"en", "de"}, "en")

		body := liaTemplateEN
		if lang == "de" {
			body = liaTemplateDE
		}

		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Content-Language", lang)

		emitView(r.Context(), auditLog, audit.EventLIAViewed, lang)

		_, _ = w.Write(body)
	})
}

// negotiateLang resolves the response language for a public disclosure
// route. Precedence: ?lang= query override → Accept-Language first match
// → fallback. Bare BCP 47 prefix match — "de-DE,de;q=0.9,en;q=0.8" picks
// "de" without parsing the full quality-value grammar (the route only
// has 2–3 supported tags so the long-form parser is overkill).
func negotiateLang(r *http.Request, supported []string, fallback string) string {
	if q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("lang"))); q != "" {
		if isSupported(q, supported) {
			return q
		}
	}

	accept := r.Header.Get("Accept-Language")
	if accept == "" {
		return fallback
	}

	for _, raw := range strings.Split(accept, ",") {
		tag := strings.ToLower(strings.TrimSpace(raw))
		// Strip quality value: "de;q=0.9" → "de".
		if idx := strings.IndexByte(tag, ';'); idx >= 0 {
			tag = strings.TrimSpace(tag[:idx])
		}
		// Strip region: "de-de" → "de".
		if idx := strings.IndexByte(tag, '-'); idx >= 0 {
			tag = tag[:idx]
		}

		if isSupported(tag, supported) {
			return tag
		}
	}

	return fallback
}

func isSupported(tag string, supported []string) bool {
	for _, s := range supported {
		if tag == s {
			return true
		}
	}

	return false
}

func emitView(ctx context.Context, auditLog *audit.Logger, name audit.EventName, lang string) {
	if auditLog == nil {
		return
	}

	auditLog.Event(ctx, name, slog.String("lang", lang))
}
