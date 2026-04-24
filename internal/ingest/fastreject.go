package ingest

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/statnive/statnive.live/internal/audit"
)

// FastRejectMiddleware short-circuits prefetch and obvious-bot requests
// with a 204 BEFORE any downstream middleware (rate limit, parsing, etc.)
// runs. This preserves the invariant that prefetches don't consume the
// per-IP rate-limit budget and don't allocate a request body buffer
// (CLAUDE.md Architecture Rule 6 + PLAN.md doc 24 §Sec 1.6).
//
// Order matters: chi.Router.Use(FastRejectMiddleware(...)) BEFORE the
// rate-limit middleware. Method enforcement (POST-only) happens here too
// so a `GET /api/event` from a probe doesn't burn a rate-limit slot.
func FastRejectMiddleware(auditLog *audit.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

				return
			}

			ua := r.Header.Get("User-Agent")
			if reason := fastReject(r.Header, ua); reason != "" {
				if auditLog != nil {
					auditLog.Event(emptyOrCtx(r), audit.EventFastReject,
						slog.String("reason", reason),
						slog.String("ua", truncate(ua, 120)),
					)
				}

				w.WriteHeader(http.StatusNoContent)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// emptyOrCtx returns the request context if non-nil, else Background.
// Belt-and-suspenders for tests that pass nil-context requests; httptest
// always sets one, so this never branches in production.
func emptyOrCtx(r *http.Request) context.Context {
	if r == nil || r.Context() == nil {
		return context.Background()
	}

	return r.Context()
}
