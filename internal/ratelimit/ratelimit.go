// Package ratelimit wraps go-chi/httprate with a key function that
// matches the ingest handler's client-IP extraction (True-Client-IP →
// CF-Connecting-IP → X-Real-IP → rightmost X-Forwarded-For). Without
// the shared key, rate-limit decisions would diverge from audit-log
// decisions about which client sent the request.
package ratelimit

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/httprate"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/ingest"
)

// Middleware returns a chi middleware that rate-limits by client IP.
// `requestsPerWindow` is the steady-state cap; httprate divides time
// into `window`-sized buckets and resets per bucket.
//
// On 429 we emit one audit.ratelimit.exceeded event with the keyed IP +
// request path so operators can identify abusive clients without
// instrumenting separate metrics for v1.
func Middleware(requestsPerWindow int, window time.Duration, auditLog *audit.Logger) (func(http.Handler) http.Handler, error) {
	if requestsPerWindow <= 0 {
		return nil, errors.New("ratelimit: requestsPerWindow must be > 0")
	}

	if window <= 0 {
		return nil, errors.New("ratelimit: window must be > 0")
	}

	limiter := httprate.NewRateLimiter(
		requestsPerWindow,
		window,
		httprate.WithKeyFuncs(httprate.KeyByIP, keyByClientIP),
		httprate.WithLimitHandler(limitHandler(auditLog)),
	)

	return limiter.Handler, nil
}

// keyByClientIP is the chi-compatible key function backed by ingest.ClientIP.
// We chain it after httprate.KeyByIP via WithKeyFuncs — httprate composes
// the keys, so a single client's RemoteAddr + ClientIP both contribute,
// preventing trivial header-spoofing from sidestepping the limiter
// without rejecting legit traffic that lacks proxy headers.
func keyByClientIP(r *http.Request) (string, error) {
	return ingest.ClientIP(r), nil
}

// limitHandler returns the http.Handler httprate invokes on 429. We emit
// an audit event + write the canonical 429 response.
func limitHandler(auditLog *audit.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auditLog != nil {
			auditLog.Event(context.Background(), audit.EventRateLimited,
				slog.String("ip", ingest.ClientIP(r)),
				slog.String("path", r.URL.Path),
				slog.String("method", r.Method),
			)
		}

		w.Header().Set("Retry-After", "1")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
	}
}
