package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/statnive/statnive.live/internal/alerts"
	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/goals"
	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/mcp"
	"github.com/statnive/statnive.live/internal/ratelimit"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
)

// runMCP serves the read-only MCP agent surface. It performs a REDUCED boot
// (ClickHouse + cached store + sites registry + audit/alerts) — no WAL, no
// ingest pipeline, no enrichment, no daemon HTTP. It must use
// loadConfigFromPath (NOT loadConfig, which re-parses os.Args and would choke
// on the `mcp`/`serve` tokens). All logs go to STDERR because STDOUT is the
// JSON-RPC channel in stdio mode.
func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)

	var (
		transport  string
		listen     string
		allowSites string
		allowAll   bool
		configFile string
	)

	fs.StringVar(&transport, "transport", "stdio", "transport: stdio | http")
	fs.StringVar(&listen, "listen", "", "override mcp.http.listen (http transport only)")
	fs.StringVar(&allowSites, "allow-sites", "", "comma-separated site_ids the stdio operator may read")
	fs.BoolVar(&allowAll, "all-sites", false, "stdio operator may read every site (wildcard)")
	fs.StringVar(&configFile, "c", "", "path to YAML config file")
	fs.StringVar(&configFile, "config", "", "path to YAML config file (long form)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if configFile == "" {
		configFile = os.Getenv(configFileEnv)
	}

	cfg, err := loadConfigFromPath(configFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	auditLog, err := audit.New(cfg.Audit.Path)
	if err != nil {
		return fmt.Errorf("audit log: %w", err)
	}

	defer func() { _ = auditLog.Close() }()

	alertsSink, err := alerts.New(cfg.Alerts.SinkPath, cfg.Alerts.HostTag)
	if err != nil {
		return fmt.Errorf("alerts sink: %w", err)
	}

	defer func() { _ = alertsSink.Close() }()

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := storage.NewClickHouseStore(rootCtx, storage.Config{
		Addrs:    []string{cfg.ClickHouse.Addr},
		Database: cfg.ClickHouse.Database,
		Username: cfg.ClickHouse.Username,
		Password: cfg.ClickHouse.Password,
	}, logger)
	if err != nil {
		return fmt.Errorf("clickhouse: %w", err)
	}

	defer func() { _ = store.Close() }()

	// Enabled-goals snapshot powers the goals_list tool (in-memory; no CH on
	// the read path). A snapshot-load failure is non-fatal — goals_list just
	// returns an empty list.
	goalSnap, gerr := goals.NewSnapshot(rootCtx, goals.NewClickHouseStore(store.Conn(), cfg.ClickHouse.Database))
	if gerr != nil {
		logger.Warn("mcp: goals snapshot load failed; goals_list will be empty", "err", gerr)
	}

	srv := mcp.New(mcp.Config{
		Store:          storage.NewCachedStore(storage.NewClickhouseQueryStore(store), dashboardCacheCapacity),
		Registry:       sites.New(store.Conn()),
		Goals:          goalSnap,
		Concrete:       store,        // off-interface reads (event_audit) live on the concrete store
		Health:         store.Conn(), // CH liveness probe for system_health (driver.Conn has Ping)
		Build:          readBuildInfo(),
		Audit:          auditLog,
		Log:            logger,
		Alerts:         alertsSink,
		Budget:         mcpBudget(cfg),
		Version:        mcpVersion(),
		GeoEnabled:     cfg.Dashboard.GeoEnabled,
		OAuthScopes:    mcpOAuthScopes(cfg), // per-tool securitySchemes (chatgpt-app only)
		WidgetsEnabled: cfg.MCP.Widgets.Enabled,
		Now:            time.Now,
	})

	// Self-serve MCP tokens (PR-A): when enabled, dashboard-minted tokens
	// resolve via the cached CH store on the HTTP transport. Off ⇒ nil ⇒
	// only the config-static APITokens work (the stdio/air-gap contract).
	var mcpTokenStore auth.APITokenStore
	if cfg.MCP.Tokens.Enabled {
		mcpTokenStore = auth.NewCachedAPITokenStore(
			auth.NewClickHouseAPITokenStore(store.Conn(), cfg.ClickHouse.Database), 0)
	}

	switch transport {
	case "stdio", "":
		allowed, perr := parseAllowSites(allowSites)
		if perr != nil {
			return perr
		}

		actor := mcp.StdioActor(allowed, allowAll)
		logger.Info("mcp stdio serving", "all_sites", allowAll, "allow_sites", allowed)

		return srv.ServeStdio(rootCtx, os.Stdin, os.Stdout, actor)
	case "http":
		return serveMCPHTTP(rootCtx, srv, cfg, listen, mcpTokenStore, auditLog, logger)
	default:
		return fmt.Errorf("unknown --transport %q (want stdio or http)", transport)
	}
}

// serveMCPHTTP runs the opt-in inbound HTTP transport behind the bearer-auth
// chain. Default-off; loopback by default; a non-loopback bind is refused
// unless posture=saas AND TLS is configured (the first intentional posture
// branch — the air-gap/Iran builds can never be internet-exposed by config
// drift).
func serveMCPHTTP(ctx context.Context, srv *mcp.Server, cfg appConfig, listenOverride string, tokens auth.APITokenStore, auditLog *audit.Logger, logger *slog.Logger) error {
	if !cfg.MCP.HTTP.Enabled {
		return errors.New("mcp http transport disabled: set mcp.http.enabled=true")
	}

	addr := cfg.MCP.HTTP.Listen
	if listenOverride != "" {
		addr = listenOverride
	}

	tlsConfigured := cfg.MCP.HTTP.TLSCertFile != "" && cfg.MCP.HTTP.TLSKeyFile != ""

	if !mcpAddrIsLoopback(addr) {
		if cfg.Posture != "saas" {
			return fmt.Errorf("mcp http refuses non-loopback bind %q unless posture=saas", addr)
		}

		if !tlsConfigured {
			return fmt.Errorf("mcp http refuses non-loopback bind %q without TLS", addr)
		}
	}

	profile := cfg.MCP.HTTP.Profile
	if profile == "" {
		profile = "loopback"
	}

	mux := http.NewServeMux()

	// tokens (DynamicTokens) lets dashboard-minted self-serve tokens authenticate
	// on /mcp in the loopback profile — the bridge that makes the "Connect your
	// AI assistant" flow work against this endpoint (PR-D). buildMCPAuthChain
	// threads it into the loopback deps; the chatgpt-app profile ignores it (it
	// uses the OAuth resource-server verifier instead).
	protected, wellKnown, err := buildMCPAuthChain(cfg, srv.HTTPHandler(mcp.HTTPOptions{}), tokens, auditLog, logger, tlsConfigured, mcpAddrIsLoopback(addr))
	if err != nil {
		return err
	}

	if wellKnown != nil {
		// RFC 9728 protected-resource metadata for IdP/ChatGPT discovery.
		mux.Handle("/.well-known/oauth-protected-resource", wellKnown)
	}

	// Proxy-aware limiter (Gate 2 B1 parity): the public /mcp surface sits behind
	// the SaaS reverse proxy in the chatgpt-app posture, so keying on RemoteAddr
	// would collapse every client into the proxy's single bucket. ratelimit
	// .Middleware keys on the forwarded client IP (ingest.ClientIP) and emits
	// audit + the rate_limited metric on each 429. Falls back to RemoteAddr for
	// the loopback profile (no forwarded headers), so behaviour is unchanged there.
	rl, err := ratelimit.Middleware(cfg.MCP.HTTP.RateLimitPerMinute, time.Minute, ratelimit.Config{Audit: auditLog})
	if err != nil {
		return fmt.Errorf("mcp http rate limiter: %w", err)
	}

	mux.Handle("/mcp", rl(protected))

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
	}

	go func() {
		<-ctx.Done()

		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = httpSrv.Shutdown(shutCtx)
	}()

	logger.Info("mcp http serving", "addr", addr, "tls", tlsConfigured, "profile", profile)

	var serveErr error
	if tlsConfigured {
		serveErr = httpSrv.ListenAndServeTLS(cfg.MCP.HTTP.TLSCertFile, cfg.MCP.HTTP.TLSKeyFile)
	} else {
		serveErr = httpSrv.ListenAndServe()
	}

	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}

	return serveErr
}

// buildMCPAuthChain wraps the base MCP handler in the auth middleware for the
// configured profile and returns it plus an optional extra route handler (the
// RFC 9728 .well-known for chatgpt-app; nil for loopback). Split out of
// serveMCPHTTP to keep that function's complexity in check.
func buildMCPAuthChain(cfg appConfig, base http.Handler, tokens auth.APITokenStore, auditLog *audit.Logger, logger *slog.Logger, tlsConfigured, loopback bool) (http.Handler, http.HandlerFunc, error) {
	if cfg.MCP.HTTP.Profile != "chatgpt-app" {
		// loopback — static Bearer tokens (v2 contract) PLUS dashboard-minted
		// self-serve tokens via DynamicTokens (PR-D bridge; nil ⇒ static-only).
		deps := auth.MiddlewareDeps{Audit: auditLog, APITokens: buildAPITokens(cfg), ClientIPFunc: ingest.ClientIP, DynamicTokens: tokens}

		return auth.APITokenMiddleware(deps)(auth.RequireAuthenticated(auditLog)(base)), nil, nil
	}

	// SaaS-only public ChatGPT-app profile: an OAuth 2.1 resource server.
	// Fail closed — never let an air-gap/Iran build into this state.
	switch {
	case cfg.Posture != "saas":
		return nil, nil, errors.New("mcp http profile=chatgpt-app requires posture=saas")
	case !cfg.MCP.HTTP.OAuth.Enabled:
		return nil, nil, errors.New("mcp http profile=chatgpt-app requires mcp.http.oauth.enabled=true")
	case len(cfg.MCP.HTTP.OAuth.AllowedSiteIDs) == 0:
		// Fail closed — never authorize an OAuth token for every tenant.
		return nil, nil, errors.New("mcp http profile=chatgpt-app requires mcp.http.oauth.allowed_site_ids (the sites a token may read)")
	case !tlsConfigured && !loopback:
		return nil, nil, errors.New("mcp http profile=chatgpt-app requires TLS on a public bind")
	}

	// The verifier lives behind the `chatgpt_app` build tag; the default (and
	// air-gap) binary ships a stub that errors here — so no IdP/JWKS code is
	// ever compiled into those builds.
	oauthMW, err := oauthMiddleware(mcpOAuthFromConfig(cfg), logger)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp oauth: %w", err)
	}

	return oauthMW(auth.RequireAuthenticated(auditLog)(base)), wellKnownOAuthHandler(cfg), nil
}

// mcpOAuthConfig is the named view of the OAuth settings the build-tagged
// verifier + the .well-known handler consume (the config field is an
// anonymous struct, awkward to pass across functions).
type mcpOAuthConfig struct {
	Enabled             bool
	Issuer              string
	Audience            string
	JWKSURL             string
	RequiredScope       string
	ResourceMetadataURL string
	AllowedSiteIDs      []uint32 // sites a verified token may read (deployment-scoped; never wildcard)
}

func mcpOAuthFromConfig(cfg appConfig) mcpOAuthConfig {
	o := cfg.MCP.HTTP.OAuth

	return mcpOAuthConfig{
		Enabled:             o.Enabled,
		Issuer:              o.Issuer,
		Audience:            o.Audience,
		JWKSURL:             o.JWKSURL,
		RequiredScope:       o.RequiredScope,
		ResourceMetadataURL: resourceMetadataURL(o.ResourceMetadataURL, o.Audience),
		AllowedSiteIDs:      o.AllowedSiteIDs,
	}
}

// resourceMetadataURL returns the configured RFC 9728 protected-resource
// metadata URL, or derives it from the audience's origin when unset, so the 401
// WWW-Authenticate always carries a resource_metadata discovery hint (Gate 2
// H2 — ChatGPT/the IdP needs it to find the authorization server). Falls back
// to the raw configured value if the audience can't be parsed.
func resourceMetadataURL(configured, audience string) string {
	if configured != "" {
		return configured
	}

	u, err := url.Parse(audience)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return configured
	}

	return u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource"
}

// mcpOAuthScopes returns the scopes tools/list advertises in per-tool
// _meta.securitySchemes — non-empty only for an OAuth-enabled chatgpt-app
// deployment, so the v2 loopback/stdio surface stays byte-identical.
func mcpOAuthScopes(cfg appConfig) []string {
	if cfg.MCP.HTTP.Profile != "chatgpt-app" || !cfg.MCP.HTTP.OAuth.Enabled {
		return nil
	}

	if s := cfg.MCP.HTTP.OAuth.RequiredScope; s != "" {
		return []string{s}
	}

	return []string{"analytics:read"}
}

// wellKnownOAuthHandler serves RFC 9728 OAuth Protected Resource Metadata so
// ChatGPT/the IdP can discover the authorization server. Static JSON — no
// crypto, no outbound — safe in every build.
func wellKnownOAuthHandler(cfg appConfig) http.HandlerFunc {
	o := cfg.MCP.HTTP.OAuth

	body, _ := json.Marshal(map[string]any{
		"resource":                 o.Audience,
		"authorization_servers":    []string{o.Issuer},
		"scopes_supported":         mcpOAuthScopes(cfg),
		"bearer_methods_supported": []string{"header"},
	})

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

// mcpBudget maps the typed config into the mcp package's BudgetConfig.
func mcpBudget(cfg appConfig) mcp.BudgetConfig {
	return mcp.BudgetConfig{
		CallsPerMin:         cfg.MCP.Budget.CallsPerMin,
		RowsPerMin:          cfg.MCP.Budget.RowsPerMin,
		CallsPerSession:     cfg.MCP.Budget.CallsPerSession,
		RowsPerSession:      cfg.MCP.Budget.RowsPerSession,
		DistinctSitesPerMin: cfg.MCP.Budget.DistinctSitesPerMin,
		WildcardFactor:      cfg.MCP.Budget.WildcardTierFactor,
	}
}

// mcpVersion returns the build version (serverInfo.version), defaulting to
// "dev" when no VCS/module version is embedded.
func mcpVersion() string {
	if v := readBuildInfo().Version; v != "" {
		return v
	}

	return "dev"
}

// parseAllowSites parses a comma-separated site_id list for --allow-sites.
func parseAllowSites(csv string) ([]uint32, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil, nil
	}

	var out []uint32

	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("--allow-sites: %q is not a positive site_id", part)
		}

		out = append(out, uint32(n))
	}

	return out, nil
}

// mcpAddrIsLoopback reports whether addr binds only the loopback interface.
// A bare ":8081" (all interfaces) is NOT loopback.
func mcpAddrIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	if host == "" {
		return false
	}

	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}
