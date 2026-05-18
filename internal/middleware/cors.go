package middleware

import (
	"context"
	"net/http"
	"strings"
)

// CORS allowlist headers + preflight contract for Stage-4 cross-origin
// SaaS consent flow. The middleware echoes the request Origin into
// Access-Control-Allow-Origin ONLY when the supplied lookup resolves
// it to a known site_id (i.e. the operator registered the origin via
// the Stage-4-A admin allowlist).
//
// Threat model (Stage-4 plan §7):
//   - Origin spoofing: browser-stamped, can't be set from cross-site JS.
//   - Unvalidated reflection (Strapi CVE-2025): we never echo without a
//     positive lookup hit.
//   - app.statnive.live self-allowlist: boot pre-flight in main.go
//     refuses to start if any operator registers the SaaS host itself.
//   - DDoS via unknown Origin: hot path is O(1) atomic.Pointer map
//     read; rebuilds only happen on admin write or SIGHUP.

// corsAllowedHeaders is the closed set of request headers the CORS
// preflight admits. X-CSRF-Token is needed for /api/privacy/*;
// X-Statnive-Site is the Stage-4-B same-origin /privacy fallback
// signal; Content-Type covers the JSON body the consent endpoints
// receive.
const corsAllowedHeaders = "Content-Type, X-CSRF-Token, X-Statnive-Site"

// corsAllowedMethods is the closed set of methods the privacy/legal
// surfaces use. PUT is included for forward-compat with v1.1 DSAR
// flows; DELETE is not needed today.
const corsAllowedMethods = "GET, POST, OPTIONS"

// corsMaxAge caps how long a browser caches a successful preflight.
// 10 minutes is short enough that a misconfigured allowlist update
// propagates quickly (operator removes an origin → preflight starts
// failing within 10 min) and far below Firefox's 86400 ceiling.
const corsMaxAge = "600"

// OriginResolver resolves an Origin header value to a site_id. Hot-
// path closure produced by sites.OriginIndex.Resolver(). Returns
// (0, false) for unknown / empty / null Origins so the middleware
// can short-circuit without taking a dep on the sites package.
type OriginResolver func(origin string) (uint32, bool)

// ctxKey is package-local to keep the type unexported but the value
// access points trivially testable.
type ctxKey int

const (
	// ctxKeySiteFromOrigin is the request-context key under which CORS
	// stashes the site_id it resolved from the Origin header.
	// Downstream handlers (privacy/handlers.go::resolveSiteAndCookie)
	// read this instead of re-doing the Origin lookup.
	ctxKeySiteFromOrigin ctxKey = iota + 1
)

// SiteIDFromOriginContext extracts the site_id stashed by CORS. Returns
// (0, false) when the request didn't go through the CORS middleware
// (same-origin path) or had no Origin to resolve.
func SiteIDFromOriginContext(ctx context.Context) (uint32, bool) {
	v := ctx.Value(ctxKeySiteFromOrigin)
	if v == nil {
		return 0, false
	}

	id, ok := v.(uint32)

	return id, ok
}

// CORS returns a chi-compatible middleware that handles preflight +
// echoes Access-Control-Allow-* headers for resolved Origins. Behaviour:
//
//   - Empty / "null" Origin → pass through unchanged (same-origin or
//     non-browser request). resolveSiteAndCookie falls back to Host.
//   - Origin resolved → write ACAO/ACAC/Vary/ACAH/ACAM/Max-Age headers,
//     stash site_id in context, chain to next.
//   - Origin unresolved → on OPTIONS return 403; on a real request,
//     pass through WITHOUT CORS headers so the browser blocks the
//     response (defence-in-depth: even if upstream auth somehow
//     bypassed, the browser refuses to expose the response to JS).
//   - Sec-Fetch-Site: cross-site → cross-validate Origin is in
//     allowlist. cross-site with unresolved Origin → 403.
//
// The middleware is designed to be Use()'d on the /privacy + /api/
// privacy/* route group only; mounting it globally would expose admin
// endpoints to credentialed cross-origin requests, which is wrong.
func CORS(resolve OriginResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Empty Origin = same-origin top-level navigation or a
			// non-browser caller. Pass through; resolveSiteAndCookie
			// downstream picks site_id from r.Host.
			if origin == "" {
				// Vary: Origin still set so caches don't serve a
				// no-Origin response to a Origin-bearing request.
				w.Header().Add("Vary", "Origin")
				next.ServeHTTP(w, r)

				return
			}

			// Stage-4 plan §3 — reject the literal "null" Origin
			// (file://, sandboxed iframes, opaque redirects). No
			// legitimate operator page sends null.
			if origin == "null" {
				w.Header().Add("Vary", "Origin")
				http.Error(w, "origin not allowed", http.StatusForbidden)

				return
			}

			// Origin → site_id lookup (atomic.Pointer map read; ~20 ns
			// + zero alloc on the hot path with browser-canonical
			// Origins).
			siteID, ok := resolve(origin)
			if !ok {
				w.Header().Add("Vary", "Origin")

				if r.Method == http.MethodOptions {
					http.Error(w, "origin not allowed", http.StatusForbidden)

					return
				}

				// Real request from unresolved Origin: chain without
				// CORS headers. Browser will refuse to expose the
				// response. Server-side handlers still see the
				// request (defence layers, not gatekeeper).
				next.ServeHTTP(w, r)

				return
			}

			// Sec-Fetch-Site cross-validation (Stage-4 plan §3 +
			// validation §3). When the browser advertises the
			// request is cross-site, the resolved Origin MUST be in
			// the allowlist — which we already confirmed above. The
			// header is informational here; we use it to refuse
			// requests whose Sec-Fetch-Site says same-origin but
			// whose Origin header points elsewhere (mismatch =
			// indicator of header injection on a misbehaving proxy).
			if sfs := r.Header.Get("Sec-Fetch-Site"); sfs == "same-origin" && !sameOriginMatch(r, origin) {
				w.Header().Add("Vary", "Origin")
				http.Error(w, "origin mismatch", http.StatusForbidden)

				return
			}

			// Resolved + cross-validated: write CORS headers.
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Headers", corsAllowedHeaders)
			h.Set("Access-Control-Allow-Methods", corsAllowedMethods)
			h.Set("Access-Control-Max-Age", corsMaxAge)

			// Preflight short-circuits — never chain to next.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)

				return
			}

			ctx := context.WithValue(r.Context(), ctxKeySiteFromOrigin, siteID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// sameOriginMatch reports whether the request's Host (or
// X-Forwarded-Host fronting through a reverse proxy) equals the
// scheme://authority of the supplied origin. Used only by the
// Sec-Fetch-Site=same-origin cross-check.
func sameOriginMatch(r *http.Request, origin string) bool {
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}

	if host == "" {
		return false
	}

	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}

	return strings.EqualFold(origin, scheme+"://"+host)
}
