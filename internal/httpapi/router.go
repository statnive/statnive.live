package httpapi

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/admin"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/dashboard"
)

// BuildRouter wires every route the daemon serves onto a fresh *chi.Mux from
// pre-built deps. The middleware ORDER and the conditional-mount set here are
// load-bearing and must stay byte-equivalent to the pre-extraction run() block
// — TestBuildRouter_GoldenRouteSet pins the route set, TestBuildRouter_Middleware
// Order pins the chain behaviour. Conditional groups mount when their flag is
// on OR in SpecMode (so the generated contract covers the full surface); in
// production (SpecMode=false) the mount set is identical to before.
func BuildRouter(d RouterDeps) (*chi.Mux, error) {
	router := chi.NewRouter()

	// corsMW MUST run before fast-reject in this group: OPTIONS preflights
	// would otherwise 405 on fast-reject's POST-only check before they reach
	// the route. Dashboard routes share the rate limiter but skip fast-reject
	// (operators don't send tracker prefetches). /healthz stays
	// unconditionally reachable for probes. Back-pressure sits AFTER rate-limit
	// (abusive clients still hit 429 first) but BEFORE the handler.
	router.Group(func(r chi.Router) {
		r.Use(d.CORS)
		r.Use(d.FastReject)
		r.Use(d.RateLimit)
		r.Use(d.Backpressure)

		// The OPTIONS binding is required so chi accepts the method — corsMW
		// intercepts the preflight and returns 204 before the handler runs.
		r.Method(http.MethodPost, "/api/event", d.Ingest)
		r.Method(http.MethodOptions, "/api/event", d.Ingest)
	})

	// Login has its own per-IP rate-limit independent of the global stats
	// limiter so legitimate stats traffic can't starve login attempts. Logout
	// is un-auth'd by design — a client with a stale cookie should still be
	// able to clear it.
	router.Group(func(r chi.Router) {
		r.Use(d.LoginRateLimit)
		r.Method(http.MethodPost, "/api/login", d.AuthLogin)
	})

	router.Method(http.MethodPost, "/api/logout", d.AuthLogout)

	router.Group(func(r chi.Router) {
		r.Use(d.Session)
		r.Use(d.APIToken)
		r.Use(d.RequireAuthed)
		r.Method(http.MethodGet, "/api/user", d.AuthMe)
	})

	// Dashboard listing — session OR api-token auth, admin+viewer+api roles.
	// /api/sites filters its response inline by actor grants.
	router.Group(func(r chi.Router) {
		r.Use(d.RateLimit)
		r.Use(d.Session)
		r.Use(d.APIToken)
		r.Use(d.RequireAuthed)
		r.Use(auth.RequireRole(d.AuditLog, auth.RoleAdmin, auth.RoleViewer, auth.RoleAPI))
		r.Use(auth.HydrateActorGrants(d.UserSites))

		dashboard.MountSiteListing(r, d.DashboardDeps)
	})

	// Dashboard site-scoped reads — same auth stack PLUS
	// RequireDashboardSiteAccess so every /api/stats/* and
	// /api/realtime/visitors call is grant-checked against the requested
	// ?site=N. OWASP A01:2021 — cross-tenant IDOR fix.
	router.Group(func(r chi.Router) {
		r.Use(d.RateLimit)
		r.Use(d.Session)
		r.Use(d.APIToken)
		r.Use(d.RequireAuthed)
		r.Use(auth.RequireRole(d.AuditLog, auth.RoleAdmin, auth.RoleViewer, auth.RoleAPI))
		// RoleAPI is the floor — matches the role allowlist above. All three
		// roles read dashboards; the per-site grant check runs independently of
		// the role hierarchy.
		r.Use(auth.RequireDashboardSiteAccess(d.AuditLog, d.UserSites, auth.RoleAPI))

		dashboard.MountSiteScoped(r, d.DashboardDeps)
	})

	// Self-serve MCP token endpoints. Mounted only when mcp.tokens.enabled
	// (or in SpecMode). Stack: mint limiter → session/api-token auth →
	// require-authed → role floor (admin/viewer) → HydrateActorGrants.
	if d.SpecMode || d.Flags.McpTokensEnabled {
		router.Group(func(r chi.Router) {
			r.Use(d.McpTokenRateLimit)
			r.Use(d.Session)
			r.Use(d.APIToken)
			r.Use(d.RequireAuthed)
			r.Use(auth.RequireRole(d.AuditLog, auth.RoleAdmin, auth.RoleViewer))
			r.Use(auth.HydrateActorGrants(d.UserSites))

			dashboard.MountMCPTokens(r, d.MCPTokenDeps)
		})
	}

	router.Group(func(r chi.Router) {
		r.Use(d.RateLimit)
		r.Use(d.Session)
		r.Use(d.APIToken)
		r.Use(d.RequireAuthed)

		if d.Flags.PerSiteAdmin && d.UserSites != nil {
			r.Use(auth.RequireSiteRole(d.AuditLog, d.UserSites, auth.RoleAdmin))
		} else {
			r.Use(auth.RequireRole(d.AuditLog, auth.RoleAdmin))
		}

		admin.Mount(r, d.AdminDeps)
	})

	// OAuth 2.1 authorization server (PR-E). No-op in the default/air-gap
	// build (the injected closure returns nil unless enabled). Mounted on the
	// main router so /authorize shares the dashboard origin + session.
	if err := d.MountOAuthAS(router); err != nil {
		return nil, fmt.Errorf("mount oauth as: %w", err)
	}

	router.Method(http.MethodGet, "/healthz", d.Health)

	// /metrics — Prometheus-text counters; bearer-auth gated, empty token
	// returns 404 (the gating lives inside the handler).
	router.Method(http.MethodGet, "/metrics", d.Metrics)

	// /api/about — unauthenticated build + third-party attribution surface.
	router.Method(http.MethodGet, "/api/about", d.About)

	// /legal/lia + /legal/dpa — public templates (no auth).
	router.Method(http.MethodGet, "/legal/lia", d.LIA)
	router.Method(http.MethodGet, "/legal/dpa", d.DPA)

	// Stage 2 — visitor-facing GDPR surface. Three route groups, each gated by
	// its own config flag (forced on in SpecMode). corsMW is reused here.
	if d.SpecMode || d.Flags.PrivacyPage {
		router.Method(http.MethodGet, "/privacy", d.CORS(d.PrivacyPage))
		router.Method(http.MethodOptions, "/privacy", d.CORS(d.PrivacyPage))
	}

	if d.SpecMode || d.Flags.LegalRoutes {
		router.Method(http.MethodGet, "/legal/privacy-policy/{lang}", d.PrivacyPolicy)
	}

	if d.SpecMode || d.Flags.PrivacyAPI {
		router.Group(func(r chi.Router) {
			r.Use(d.CORS)
			r.Use(d.RequireCSRF)
			r.Method(http.MethodPost, "/api/privacy/opt-out", d.PrivacyOptOut)
			r.Method(http.MethodOptions, "/api/privacy/opt-out", d.PrivacyOptOut)
			r.Method(http.MethodGet, "/api/privacy/access", d.PrivacyAccess)
			r.Method(http.MethodOptions, "/api/privacy/access", d.PrivacyAccess)
			r.Method(http.MethodPost, "/api/privacy/erase", d.PrivacyErase)
			r.Method(http.MethodOptions, "/api/privacy/erase", d.PrivacyErase)
			r.Method(http.MethodPost, "/api/privacy/consent", d.PrivacyConsent)
			r.Method(http.MethodOptions, "/api/privacy/consent", d.PrivacyConsent)
		})
	}

	// First-party tracker — static blob, safe unauthenticated.
	router.Method(http.MethodGet, "/tracker.js", d.Tracker)

	// Public coming-soon page at GET / (+ HEAD). Independent of SPAEnabled.
	router.Method(http.MethodGet, "/", d.Landing)
	router.Method(http.MethodHead, "/", d.Landing)
	router.Method(http.MethodGet, "/favicon.ico", d.Favicon)
	router.Method(http.MethodHead, "/favicon.ico", d.Favicon)

	// Embedded Preact dashboard SPA at /app/*. Auth is enforced at /api/* by
	// the session + api-token middleware; the SPA shell is safe to serve
	// unauthenticated because it can't reach stats without a valid session.
	if d.SpecMode || d.Flags.SPAEnabled {
		router.Method(http.MethodGet, "/app", http.RedirectHandler("/app/", http.StatusFound))
		router.Mount("/app/", http.StripPrefix("/app", d.Spa))
	}

	return router, nil
}
