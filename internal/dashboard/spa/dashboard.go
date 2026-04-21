// Package spa serves the statnive-live dashboard SPA (Preact + signals,
// built by Vite under ../web/) via //go:embed. First-party delivery
// enforces the air-gap invariant (CLAUDE.md Isolation rule) — the SPA is
// part of the binary, not loaded from a CDN.
//
// Security posture: every response carries Content-Security-Policy +
// X-Content-Type-Options + Referrer-Policy so the air-gap is enforced
// in-browser, not just at build time. The complementary build-time gate
// `make web-airgap-grep` scans web/dist/ for any CDN URLs that slipped
// past code review.
//
// Auth posture: the SPA is gated off in production until Phase 2b lands
// bcrypt sessions + RBAC. cmd/statnive-live/main.go only registers the
// /app/* route when cfg.Dashboard.SPAEnabled is true (dev default false).
// When enabled, the SPA reads the bearer token from a <meta> tag this
// package rewrites into index.html at request time.
package spa

import (
	"bytes"
	"embed"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

const bearerPlaceholder = "STATNIVE_BEARER_PLACEHOLDER" //nolint:gosec // placeholder string, not a credential; rewritten per-request.

// Config wires the optional bearer token into the SPA bootstrap HTML.
// Empty token is a valid dev-mode value — the middleware fallback at
// internal/dashboard/auth.go:BearerTokenMiddleware treats empty as no-op.
type Config struct {
	// BearerToken is written into the <meta name="statnive-bearer"> tag
	// on every /app/ request so the SPA can attach Authorization: Bearer
	// on API calls. Phase 2b replaces this whole mechanism with sessions.
	BearerToken string
}

// Handler returns an http.Handler that serves the embedded SPA at a
// caller-chosen mount point. Caller strips the mount prefix before
// invoking this handler (see cmd/statnive-live/main.go for wiring).
func Handler(cfg Config) (http.Handler, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}

	indexTemplate, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, err
	}

	// Pre-render the bearer-injected index ONCE at construction time —
	// BearerToken is immutable across the server lifetime so rewriting
	// on every request would allocate ~1 KB per /app/ hit for no reason.
	renderedIndex := bytes.Replace(indexTemplate, []byte(bearerPlaceholder), []byte(cfg.BearerToken), 1)

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSecurityHeaders(w)

		upath := r.URL.Path

		// /assets/* paths go through the file server — it handles 404
		// for missing assets (must NOT fall through to index, else real
		// 404s get shadowed by HTML).
		if strings.HasPrefix(upath, "/assets/") {
			// Vite's hashed filenames make long-cache safe (content
			// change = new hash = new URL).
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			fileServer.ServeHTTP(w, r)

			return
		}

		// Non-asset real files (e.g. /favicon.ico) also via file server.
		if hasAsset(sub, upath) {
			fileServer.ServeHTTP(w, r)

			return
		}

		// Everything else is SPA routing — serve the pre-rendered index
		// (token already injected). Vite's single-page build means any
		// route falls back to the client-side router in the SPA.
		serveIndex(w, renderedIndex)
	}), nil
}

// writeSecurityHeaders emits the 3 air-gap-enforcing headers + the CSP.
// CSP notes:
//   - `default-src 'self'` — no external origins for scripts/connects/images
//   - `connect-src 'self'` — fetch() can only hit the same origin (API)
//   - `font-src 'self'` — self-hosted fonts from web/public/fonts/
//   - `style-src 'self' 'unsafe-inline'` — Vite injects critical CSS inline;
//     revisit when we move to hashed/SSR'd styles
//   - `img-src 'self' data:` — allow data: URIs for tiny inline SVGs/PNGs
//   - `base-uri 'self'` — stops <base> injection attacks
func writeSecurityHeaders(w http.ResponseWriter) {
	const csp = "default-src 'self'; " +
		"connect-src 'self'; " +
		"font-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; " +
		"base-uri 'self'; " +
		"frame-ancestors 'none'"
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
}

// serveIndex writes the pre-rendered index HTML. The bearer token was
// already injected at Handler() construction (immutable over lifetime),
// so this path has zero per-request allocation beyond the writer.
func serveIndex(w http.ResponseWriter, rendered []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")

	_, _ = io.Copy(w, bytes.NewReader(rendered))
}

// hasAsset checks whether upath resolves to a file in the embedded FS.
// Keeps the SPA fallback from shadowing real asset 404s.
func hasAsset(sub fs.FS, upath string) bool {
	clean := strings.TrimPrefix(upath, "/")
	if clean == "" {
		return false
	}

	f, err := sub.Open(clean)
	if err != nil {
		return false
	}

	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}

	return true
}

// ErrNoIndex is returned when the embedded dist/ doesn't contain
// index.html — typically because `npm run build` hasn't been run or the
// Makefile's `web-build` step was skipped. Surfaced during Handler()
// construction so the binary fails fast at boot rather than 500-ing
// every request.
var ErrNoIndex = errors.New("spa: dist/index.html missing — run `make web-build` or `npm --prefix web run build`")
