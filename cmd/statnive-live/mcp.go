package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/httprate"

	"github.com/statnive/statnive.live/internal/alerts"
	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/goals"
	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/mcp"
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
		Store:      storage.NewCachedStore(storage.NewClickhouseQueryStore(store), dashboardCacheCapacity),
		Registry:   sites.New(store.Conn()),
		Goals:      goalSnap,
		Concrete:   store,        // off-interface reads (event_audit) live on the concrete store
		Health:     store.Conn(), // CH liveness probe for system_health (driver.Conn has Ping)
		Build:      readBuildInfo(),
		Audit:      auditLog,
		Log:        logger,
		Alerts:     alertsSink,
		Budget:     mcpBudget(cfg),
		Version:    mcpVersion(),
		GeoEnabled: cfg.Dashboard.GeoEnabled,
		Now:        time.Now,
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

	// DynamicTokens lets dashboard-minted (self-serve) tokens authenticate on
	// /mcp, not just the config-static APITokens — the bridge that makes the
	// "Connect your AI assistant" flow actually work against this endpoint.
	deps := auth.MiddlewareDeps{Audit: auditLog, APITokens: buildAPITokens(cfg), ClientIPFunc: ingest.ClientIP, DynamicTokens: tokens}

	handler := srv.HTTPHandler(mcp.HTTPOptions{})
	authed := auth.APITokenMiddleware(deps)(auth.RequireAuthenticated(auditLog)(handler))
	limited := httprate.LimitByIP(cfg.MCP.HTTP.RateLimitPerMinute, time.Minute)(authed)

	mux := http.NewServeMux()
	mux.Handle("/mcp", limited)

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

	logger.Info("mcp http serving", "addr", addr, "tls", tlsConfigured)

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
