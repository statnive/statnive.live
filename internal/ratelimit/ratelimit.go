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
	"github.com/statnive/statnive.live/internal/metrics"
)

// Config bundles the optional knobs Middleware accepts beyond the
// required (requests, window, audit). New since the load-gate work added
// the metrics counter + the load-test allowlist.
type Config struct {
	// Audit receives audit.EventRateLimited on every 429. Optional.
	Audit *audit.Logger
	// Metrics receives the rate_limited counter increment on every 429.
	// Optional — nil-safe.
	Metrics *metrics.Registry
	// AllowlistedIPs are exempt from the rate-limit ladder. Used during
	// load tests to keep a single generator IP from tripping the 100
	// req/s/IP fallback. Allowlisted IPs do NOT bypass any other gate
	// (UA, hostname, payload, WAL). Empty = no allowlist (production
	// default). Strings should be canonical IPv4/IPv6 (no zone suffix).
	AllowlistedIPs []string
}

// Middleware returns a chi middleware that rate-limits by client IP.
// `requestsPerWindow` is the steady-state cap; httprate divides time
// into `window`-sized buckets and resets per bucket.
//
// On 429 we emit one audit.ratelimit.exceeded event with the keyed IP +
// request path so operators can identify abusive clients.
func Middleware(requestsPerWindow int, window time.Duration, cfg Config) (func(http.Handler) http.Handler, error) {
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
		httprate.WithLimitHandler(limitHandler(cfg.Audit, cfg.Metrics)),
	)

	allow := buildAllowlist(cfg.AllowlistedIPs)

	return func(next http.Handler) http.Handler {
		gated := limiter.Handler(next)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allow[ingest.ClientIP(r)] {
				next.ServeHTTP(w, r)

				return
			}

			gated.ServeHTTP(w, r)
		})
	}, nil
}

func buildAllowlist(ips []string) map[string]bool {
	if len(ips) == 0 {
		return nil
	}

	out := make(map[string]bool, len(ips))
	for _, ip := range ips {
		if ip != "" {
			out[ip] = true
		}
	}

	return out
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
//
// Raw client IP is intentionally NOT logged to the audit sink — Privacy
// Rule 1 (no raw IP persisted) applies equally to audit. Path + method
// plus the aggregate rate-limit counters surfaced via /metrics are
// sufficient for ops to correlate an incident. The rate-limiter itself
// still keys on the IP for the limiting decision — only the audit-log
// serialization loses it.
func limitHandler(auditLog *audit.Logger, reg *metrics.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auditLog != nil {
			auditLog.Event(context.Background(), audit.EventRateLimited,
				slog.String("path", r.URL.Path),
				slog.String("method", r.Method),
			)
		}

		reg.IncDropped(metrics.ReasonRateLimited)

		w.Header().Set("Retry-After", "1")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
	}
}
