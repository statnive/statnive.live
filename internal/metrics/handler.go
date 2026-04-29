package metrics

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Handler returns an http.Handler that serves the registry in Prometheus
// text format. Token gate matches via constant-time compare.
//
// If token is empty, the handler returns 404 — effectively disabling the
// endpoint. This is the safe default for production binaries that haven't
// opted into metrics exposure.
//
// Token transport: Authorization: Bearer <token>. Operators set
// STATNIVE_METRICS_TOKEN as a systemd Environment= drop-in; the
// observability VPS scrape config injects the same value.
func Handler(reg *Registry, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			http.NotFound(w, r)

			return
		}

		got := bearerToken(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)

			return
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4")

		if err := reg.WriteText(w); err != nil {
			// Body partially flushed already — best we can do is log.
			// Caller's slog instance not available here; the http.Server
			// access log will record the partial response.
			return
		}
	})
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}

	return strings.TrimPrefix(header, prefix)
}
