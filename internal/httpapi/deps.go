// Package httpapi holds the single importable router-construction seam.
//
// BuildRouter wires every HTTP route the daemon serves onto a *chi.Mux from
// pre-built dependencies. It exists so two callers share ONE route table:
//   - cmd/statnive-live/run() — production wiring (SpecMode=false).
//   - cmd/specgen            — walks the router to derive the OpenAPI contract
//     (SpecMode=true), with stub/no-op deps and every conditional group forced
//     on, so the generated spec covers the full surface.
//
// The package is deliberately import-light: callers pass already-constructed
// http.Handlers, dep structs, and middlewares, so BuildRouter never dials
// ClickHouse, reads an embed, or returns a construction error (the only error
// path is the injected MountOAuthAS closure). All error/dial-prone construction
// stays in run(), behind the same flags as today, so a flags-OFF production
// boot is byte-identical.
package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/admin"
	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/dashboard"
)

// Middleware is a chi/net-http middleware constructor result.
type Middleware = func(http.Handler) http.Handler

// RouterFlags mirrors the config flags that gate conditional route groups.
// In production each is the live cfg value; in SpecMode every group is forced
// on regardless (see BuildRouter), so the flags only matter for SpecMode=false.
type RouterFlags struct {
	McpTokensEnabled bool // cfg.MCP.Tokens.Enabled (&& mcpTokenStore != nil)
	PerSiteAdmin     bool // cfg.Features.PerSiteAdmin
	PrivacyPage      bool // cfg.Privacy.PrivacyPage
	LegalRoutes      bool // cfg.Privacy.LegalRoutes
	PrivacyAPI       bool // cfg.Privacy.PrivacyAPI
	SPAEnabled       bool // cfg.Dashboard.SPAEnabled
}

// RouterDeps bundles everything BuildRouter wires. Every field is built by the
// caller. Handlers are passed as http.Handler (so this package needs no
// import of ingest/privacy/legal/health/metrics/about/tracker/landing/spa);
// the role middlewares are the only things BuildRouter constructs itself
// (pure, no I/O), from AuditLog + UserSites.
type RouterDeps struct {
	AuditLog *audit.Logger

	// Middlewares (built in run(); config/error/dial-derived).
	CORS              Middleware
	RateLimit         Middleware
	LoginRateLimit    Middleware
	McpTokenRateLimit Middleware
	Session           Middleware
	APIToken          Middleware
	RequireAuthed     Middleware
	RequireCSRF       Middleware
	FastReject        Middleware
	Backpressure      Middleware

	// Ingest + auth handlers.
	Ingest     http.Handler
	AuthLogin  http.Handler
	AuthLogout http.Handler
	AuthMe     http.Handler

	// Dashboard + admin dep structs (carry unexported main types like
	// goalAutocompleter, so they must be built in run() and passed whole).
	UserSites     auth.SitesStore // nil when per_site_admin off
	DashboardDeps dashboard.Deps
	AdminDeps     admin.Deps
	MCPTokenDeps  dashboard.MCPTokenDeps

	// Operational / public handlers.
	Health  http.Handler
	Metrics http.Handler
	About   http.Handler
	LIA     http.Handler
	DPA     http.Handler
	Tracker http.Handler
	Landing http.Handler
	Favicon http.Handler

	// Privacy + legal-policy handlers (nil when their flag is off in
	// production; non-nil stubs in SpecMode).
	PrivacyPolicy  http.Handler
	PrivacyPage    http.Handler
	PrivacyOptOut  http.Handler
	PrivacyAccess  http.Handler
	PrivacyErase   http.Handler
	PrivacyConsent http.Handler

	// SPA shell (nil when spa_enabled off in production).
	Spa http.Handler

	// MountOAuthAS registers the OAuth-AS routes (chatgpt_app build) or is a
	// no-op (default build / SpecMode). Returns an error that BuildRouter
	// propagates to abort boot, exactly as the inline mount did.
	MountOAuthAS func(chi.Router) error

	Flags    RouterFlags
	SpecMode bool
}
