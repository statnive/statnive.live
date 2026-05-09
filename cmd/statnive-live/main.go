// statnive-live — Go single binary + ClickHouse analytics platform.
// Phase 2a build: TLS 1.3 with manual PEM + SIGHUP reload, NAT-aware
// httprate rate limiting on /api/event, JSONL audit log file sink with
// SIGHUP-aware reopen for logrotate. Auth + RBAC + systemd hardening land
// in subsequent slices per PLAN.md.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"

	"github.com/statnive/statnive.live/internal/about"
	"github.com/statnive/statnive.live/internal/admin"
	"github.com/statnive/statnive.live/internal/alerts"
	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/cert"
	"github.com/statnive/statnive.live/internal/config"
	"github.com/statnive/statnive.live/internal/dashboard"
	"github.com/statnive/statnive.live/internal/dashboard/spa"
	"github.com/statnive/statnive.live/internal/enrich"
	"github.com/statnive/statnive.live/internal/goals"
	"github.com/statnive/statnive.live/internal/health"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/landing"
	"github.com/statnive/statnive.live/internal/metrics"
	"github.com/statnive/statnive.live/internal/ratelimit"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
	"github.com/statnive/statnive.live/internal/tracker"
)

// dashboardCacheCapacity sizes the per-process query cache. 4096 entries
// ≈ 50 MB worst-case for typical OverviewResult sizes; adequate for v1
// + first ~100 SaaS tenants. Bump for SaaS deployments past ~1K tenants.
const dashboardCacheCapacity = 4096

const (
	bloomCapacity = 10_000_000
	bloomFPRate   = 0.001
	// masterSecretEnv is the env var name the operator sets to the master
	// secret value. Not the secret itself.
	masterSecretEnv = "STATNIVE_MASTER_SECRET" //nolint:gosec // env-var NAME, not a credential
	// configFileEnv is the env-var fallback for the -c / --config CLI
	// flag (operators who can only set env, e.g. systemd EnvironmentFile,
	// reach the same fatal-on-missing semantics as -c).
	configFileEnv = "STATNIVE_CONFIG_FILE"
)

func main() {
	// CI gate: print embed sizes and exit non-zero if any are below their
	// release-build floor. Used by `make airgap-bundle-verify` to catch the
	// LEARN.md Lesson 23 //go:embed regression class without changing the
	// binary's runtime startup behavior (which deliberately falls back to
	// inline patterns for fresh checkouts before `make refresh-bot-patterns`).
	for _, a := range os.Args[1:] {
		if a == "--check-embed-sizes" || a == "-check-embed-sizes" {
			os.Exit(checkEmbedSizes())
		}
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "statnive-live: %v\n", err)
		os.Exit(1)
	}
}

// checkEmbedSizes prints each embed's byte count to stdout and returns
// 1 if any embed is below its release-build floor, 0 otherwise. Designed
// to be called from CI right after `make build-linux` to assert the
// expected upstream JSON / asset blobs actually compiled into the binary.
func checkEmbedSizes() int {
	type embed struct {
		name     string
		size     int
		floor    int
		floorRef string
	}

	embeds := []embed{
		{"crawler-user-agents.json", enrich.CrawlerEmbedBytes(), enrich.CrawlerEmbedMinBytes(), "LEARN.md Lesson 23"},
	}

	exit := 0

	for _, e := range embeds {
		ok := e.size >= e.floor
		mark := "OK  "

		if !ok {
			mark = "FAIL"
			exit = 1
		}

		fmt.Printf("%s  %-40s  %8d B  (floor %8d B, %s)\n",
			mark, e.name, e.size, e.floor, e.floorRef)
	}

	if exit != 0 {
		fmt.Fprintln(os.Stderr, "statnive-live --check-embed-sizes: at least one embed below floor; build is broken")
	}

	return exit
}

//nolint:gocyclo,funlen // main wires 25+ subsystems linearly; splitting hides the wire order
func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	auditLog, err := audit.New(cfg.Audit.Path)
	if err != nil {
		return fmt.Errorf("audit log: %w", err)
	}

	defer func() { _ = auditLog.Close() }()

	// alertsSink is nil (no-op) when alerts.sink_path is empty — that
	// keeps the "pure-stdout" dev posture valid. Phase 8 defaults ship
	// with sink_path set in the production YAML template.
	alertsSink, err := alerts.New(cfg.Alerts.SinkPath, cfg.Alerts.HostTag)
	if err != nil {
		return fmt.Errorf("alerts sink: %w", err)
	}

	defer func() { _ = alertsSink.Close() }()

	masterSecret, err := config.LoadMasterSecret(masterSecretEnv, cfg.MasterSecretPath)
	if err != nil {
		return fmt.Errorf("master secret: %w", err)
	}

	saltMgr, err := identity.NewSaltManager(masterSecret)
	if err != nil {
		return fmt.Errorf("salt manager: %w", err)
	}

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

	migrator := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{
		Database: cfg.ClickHouse.Database,
		Cluster:  cfg.ClickHouse.Cluster,
	}, logger)

	if migErr := migrator.Run(rootCtx); migErr != nil {
		return fmt.Errorf("migrate: %w", migErr)
	}

	wal, err := ingest.NewWALWriter(ingest.WALConfig{
		Dir:      cfg.Ingest.WALDir,
		MaxBytes: cfg.Ingest.WALMaxBytes,
		AuditLog: auditLog,
	}, logger)
	if err != nil {
		return fmt.Errorf("wal: %w", err)
	}

	defer func() { _ = wal.Close() }()

	// Crash-recovery WAL replay. Events from a previous boot that
	// hadn't yet been ack'd by the consumer are persisted in the WAL.
	// Replay them straight into ClickHouse before the new consumer
	// starts, then ack-truncate so the next batch sees a clean log.
	// This closes the kill -9 → restart → zero-loss contract that
	// Phase 1 implemented but never wired.
	if replayErr := replayWAL(rootCtx, wal, store, logger); replayErr != nil {
		return fmt.Errorf("wal replay: %w", replayErr)
	}

	bloom := enrich.NewNewVisitorFilter(bloomCapacity, bloomFPRate)
	bloomPath := filepath.Join(cfg.Ingest.WALDir, "bloom.dat")

	if loadErr := bloom.LoadFrom(bloomPath); loadErr != nil {
		logger.Warn("bloom load failed; starting cold", "path", bloomPath, "err", loadErr)
	} else {
		logger.Info("bloom loaded", "path", bloomPath, "approx_entries", bloom.EstimatedCount())
	}

	// Save the bloom on shutdown — runs whether the errgroup exits cleanly
	// or with an error. Defer ordering matters: this fires after all the
	// pipeline + consumer goroutines have drained (g.Wait() unblocks first).
	defer func() {
		if saveErr := bloom.SaveTo(bloomPath); saveErr != nil {
			logger.Warn("bloom save failed", "path", bloomPath, "err", saveErr)
		} else {
			logger.Info("bloom saved", "path", bloomPath)
		}
	}()

	geoIP, err := enrich.NewGeoIPEnricher(cfg.Enrich.GeoIPBinPath, logger)
	if err != nil {
		return fmt.Errorf("geoip: %w", err)
	}

	defer func() { _ = geoIP.Close() }()

	channelMapper, err := enrich.NewChannelMapper(cfg.Enrich.SourcesPath)
	if err != nil {
		return fmt.Errorf("channel mapper: %w", err)
	}
	defer channelMapper.Close()

	burstGuard := ingest.NewBurstGuard(cfg.Ingest.MaxPageviewsPerMinutePerVisitor)

	// Phase 3c — goals snapshot. Admin CRUD writes rows to
	// statnive.goals; the in-memory Snapshot is the hot-path matcher
	// (atomic.Pointer hot-swap, zero CH round-trip per event). Admin
	// mutations call Snapshot.Reload inline; SIGHUP also triggers a
	// reload so external config flips (direct CH INSERT by an
	// operator) propagate to ingest without a restart.
	goalStore := goals.NewClickHouseStore(store.Conn(), cfg.ClickHouse.Database)

	goalSnapshot, err := goals.NewSnapshot(rootCtx, goalStore)
	if err != nil {
		return fmt.Errorf("goals snapshot: %w", err)
	}

	logger.Info("goals snapshot loaded", "count", goalSnapshot.Size())

	metricsReg := metrics.New()

	pipeline := enrich.NewPipeline(enrich.Deps{
		Salt:    saltMgr,
		Bloom:   bloom,
		GeoIP:   geoIP,
		UA:      enrich.NewUAParser(),
		Bot:     enrich.NewBotDetector(logger),
		Channel: channelMapper,
		Burst:   burstGuard,
		Goals:   goalSnapshot,
		Audit:   auditLog,
		Metrics: metricsReg,
		Logger:  logger,
	})

	// GroupSyncer sits between the handler and the consumer: handlers
	// call AppendAndWait (blocks until fsync), consumer reads from Out()
	// after the batch is durable. Sync errors terminate via os.Exit(1)
	// — fsyncgate 2018; orchestrator restarts.
	groupSyncer := ingest.NewGroupSyncer(wal, ingest.GroupConfig{}, auditLog, logger)
	defer groupSyncer.Close()

	consumer := ingest.NewConsumer(groupSyncer.Out(), wal, store, ingest.ConsumerConfig{
		BatchRows:     cfg.Ingest.BatchRows,
		BatchInterval: cfg.Ingest.BatchInterval,
		BatchMaxBytes: cfg.Ingest.BatchMaxBytes,
	}, auditLog, logger)

	registry := sites.New(store.Conn())

	cachedStore := storage.NewCachedStore(storage.NewClickhouseQueryStore(store), dashboardCacheCapacity)

	rateLimitMW, err := ratelimit.Middleware(cfg.RateLimit.RequestsPerMinute, time.Minute, ratelimit.Config{
		Audit:          auditLog,
		Metrics:        metricsReg,
		AllowlistedIPs: cfg.RateLimit.AllowlistedIPs,
	})
	if err != nil {
		return fmt.Errorf("ratelimit: %w", err)
	}

	// Phase 2b — replace the single pre-shared bearer with session
	// cookie + RBAC + optional API-token fallback. Store-level
	// CachedStore wraps the CH-backed auth store so session lookups on
	// every authenticated request are an O(1) LRU hit instead of a CH
	// round-trip. Cascade-revoke on password/role/disable is enforced
	// by the CachedStore wrapper (see internal/auth/store.go).
	authStore := auth.NewCachedStore(
		auth.NewClickHouseStore(store.Conn(), cfg.ClickHouse.Database),
		cfg.Auth.Session.CacheTTL,
	)

	if bootErr := auth.Bootstrap(rootCtx, authStore, auth.BootstrapConfig{
		Email:      os.Getenv("STATNIVE_BOOTSTRAP_ADMIN_EMAIL"),
		Password:   os.Getenv("STATNIVE_BOOTSTRAP_ADMIN_PASSWORD"),
		Username:   cfg.Auth.Bootstrap.Username,
		SiteID:     cfg.Auth.Bootstrap.SiteID,
		BcryptCost: cfg.Auth.BcryptCost,
	}, auditLog, logger); bootErr != nil {
		return fmt.Errorf("auth bootstrap: %w", bootErr)
	}

	cookieCfg := auth.SessionCookieConfig{
		Name:     cfg.Auth.Session.CookieName,
		TTL:      cfg.Auth.Session.TTL,
		Secure:   cfg.Auth.Session.Secure,
		SameSite: http.SameSiteLaxMode,
	}

	// Fail closed: operators who downgrade Secure to false under TLS
	// would silently disable the protection. The only legitimate case is
	// STATNIVE_DEV=1 local dev without TLS; production with TLS keeps
	// Secure=true.
	if !cfg.Auth.Session.Secure && os.Getenv("STATNIVE_DEV") != "1" {
		return errors.New("auth.session.secure=false requires STATNIVE_DEV=1")
	}

	apiTokens := buildAPITokens(cfg)

	authDeps := auth.MiddlewareDeps{
		Store:        authStore,
		Audit:        auditLog,
		CookieCfg:    cookieCfg,
		APITokens:    apiTokens,
		ClientIPFunc: ingest.ClientIP,
	}

	sessionMW := auth.SessionMiddleware(authDeps)
	apiTokenMW := auth.APITokenMiddleware(authDeps)
	requireAuthed := auth.RequireAuthenticated(auditLog)

	authHandlers := auth.NewHandlers(authDeps, auth.HandlersConfig{
		DefaultSiteID: cfg.Auth.DefaultSiteID,
		MasterSecret:  masterSecret,
		DemoBanner:    cfg.Auth.DemoBanner,
	}, auth.NewLockout(auth.LockoutConfig{
		MaxFails:   cfg.Auth.Lockout.Fails,
		Decay:      cfg.Auth.Lockout.Decay,
		Lockout:    cfg.Auth.Lockout.Lockout,
		MaxTracked: 10_000,
	}))

	loginRateLimitMW, err := ratelimit.Middleware(
		cfg.Auth.LoginRateLimit.Requests,
		cfg.Auth.LoginRateLimit.Window,
		ratelimit.Config{Audit: auditLog, Metrics: metricsReg},
	)
	if err != nil {
		return fmt.Errorf("login ratelimit: %w", err)
	}

	router := chi.NewRouter()

	// /api/event runs the fast-reject gate BEFORE the rate limiter so
	// prefetches + obvious-bot UAs don't burn per-IP budget. Dashboard
	// routes share the rate limiter (so abusive polling can't drain
	// ClickHouse) but skip fast-reject (operators don't send tracker
	// prefetches). /healthz stays unconditionally reachable for probes.
	router.Group(func(r chi.Router) {
		r.Use(ingest.FastRejectMiddleware(auditLog, metricsReg))
		r.Use(rateLimitMW)
		// Back-pressure gate sits AFTER rate-limit (abusive clients still
		// hit 429 first) but BEFORE the handler (so a degraded WAL
		// doesn't burn enrichment + fsync budget on events destined for
		// 503). wal-durability-review item #6.
		r.Use(ingest.BackpressureMiddleware(wal, ingest.BackpressureConfig{
			OnSample: walFillAlertEmitter(alertsSink),
			Metrics:  metricsReg,
		}))
		r.Method(http.MethodPost, "/api/event", ingest.NewHandler(ingest.HandlerConfig{
			Pipeline:        pipeline,
			WAL:             groupSyncer,
			Sites:           registry,
			MasterSecret:    masterSecret,
			Audit:           auditLog,
			Logger:          logger,
			ConsentRequired: cfg.Consent.Required,
			Metrics:         metricsReg,
		}))
	})

	// Login / logout / me. /api/login has its own per-IP rate-limit
	// (10/min default) independent of the global stats limiter so that
	// legitimate stats traffic can't starve login attempts. Logout is
	// un-auth'd by design — a client with a stale cookie should still
	// be able to clear it.
	router.Group(func(r chi.Router) {
		r.Use(loginRateLimitMW)
		r.Method(http.MethodPost, "/api/login", http.HandlerFunc(authHandlers.Login))
	})

	router.Method(http.MethodPost, "/api/logout", http.HandlerFunc(authHandlers.Logout))

	router.Group(func(r chi.Router) {
		r.Use(sessionMW)
		r.Use(apiTokenMW)
		r.Use(requireAuthed)
		r.Method(http.MethodGet, "/api/user", http.HandlerFunc(authHandlers.Me))
	})

	// Dashboard stats — session OR api-token auth, admin+viewer+api roles.
	router.Group(func(r chi.Router) {
		r.Use(rateLimitMW)
		r.Use(sessionMW)
		r.Use(apiTokenMW)
		r.Use(requireAuthed)
		r.Use(auth.RequireRole(auditLog, auth.RoleAdmin, auth.RoleViewer, auth.RoleAPI))

		dashboard.Mount(r, dashboard.Deps{
			Store:  cachedStore,
			Sites:  registry,
			Audit:  auditLog,
			Logger: logger,
		})
	})

	// Admin CRUD — admin-only. Viewers + api-tokens are rejected with
	// 403 + auth.rbac.denied audit event by RequireRole.
	router.Group(func(r chi.Router) {
		r.Use(rateLimitMW)
		r.Use(sessionMW)
		r.Use(apiTokenMW)
		r.Use(requireAuthed)
		r.Use(auth.RequireRole(auditLog, auth.RoleAdmin))
		admin.Mount(r, admin.Deps{
			Auth:     authStore,
			Goals:    goalStore,
			Snapshot: goalSnapshot,
			Sites:    registry,
			Audit:    auditLog,
			Logger:   logger,
		})
	})

	router.Method(http.MethodGet, "/healthz", health.Handler(health.Reporter{
		Store:     store,
		WAL:       wal,
		WALSyncer: groupSyncer,
		Start:     time.Now(),
	}))

	// /metrics — Prometheus-text counters for the /api/event funnel
	// (received / accepted{site_id} / dropped{reason}). Bearer-auth
	// gated by STATNIVE_METRICS_TOKEN; empty token returns 404 (default
	// production posture). Phase 7e deploy/observability scrapes this.
	router.Method(http.MethodGet, "/metrics", metrics.Handler(metricsReg, cfg.Metrics.Token))

	// /api/about — unauthenticated build + third-party attribution
	// surface. Required by CLAUDE.md License Rules for IP2Location LITE
	// CC-BY-SA-4.0 §3(a)(1); paired with LICENSE-third-party.md and the
	// dashboard footer. buildInfo is hoisted so /api/about and the
	// landing meta strip can't drift against each other.
	buildInfo := readBuildInfo()
	router.Method(http.MethodGet, "/api/about", about.Handler(buildInfo, about.DefaultAttributions()))

	// First-party tracker — bytes embedded via go:embed in internal/tracker.
	// Sits outside the dashboard auth + rate-limit groups; serves a static
	// blob that's safe to hand back unauthenticated under any traffic.
	router.Method(http.MethodGet, "/tracker.js", tracker.Handler())

	// Public coming-soon page at GET /. Independent of cfg.Dashboard.SPAEnabled
	// so the marketing surface is reachable even when the operator-facing
	// dashboard is gated off in prod. The Iranian-DC air-gap binary does not
	// register either route — no public marketing surface (Architecture C).
	// Build version is template-injected so the meta strip tracks the actual
	// shipped binary instead of drifting against a hardcoded literal.
	landingHandler := landing.Handler(landing.Config{Version: buildInfo.Version})
	router.Method(http.MethodGet, "/", landingHandler)
	router.Method(http.MethodHead, "/", landingHandler)
	faviconHandler := landing.FaviconHandler()
	router.Method(http.MethodGet, "/favicon.ico", faviconHandler)
	router.Method(http.MethodHead, "/favicon.ico", faviconHandler)

	// Embedded Preact dashboard SPA at /app/*. Auth is enforced at
	// /api/* by session + api-token middleware (see auth.CompositeAuth);
	// the SPA shell is safe to serve unauthenticated because it can't
	// reach stats without a valid session cookie.
	//
	// TODO(phase-10): /app/* must be on the bypass list for the
	// IR-country 302 redirect (research doc 26 §5.3) so the dashboard
	// is reachable from outside Iran without geo-rerouting.
	if cfg.Dashboard.SPAEnabled {
		spaHandler, spaErr := spa.Handler(spa.Config{BearerToken: cfg.Dashboard.BearerToken})
		if spaErr != nil {
			return fmt.Errorf("spa: %w", spaErr)
		}

		router.Method(http.MethodGet, "/app", http.RedirectHandler("/app/", http.StatusFound))
		router.Mount("/app/", http.StripPrefix("/app", spaHandler))

		logger.Info("spa enabled", "mount", "/app/", "bearer_set", cfg.Dashboard.BearerToken != "")
	}

	tlsLoader, err := newTLSLoader(cfg, auditLog, logger)
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       60 * time.Second,
	}

	if tlsLoader != nil {
		srv.TLSConfig = &tls.Config{
			MinVersion:     tls.VersionTLS13,
			GetCertificate: tlsLoader.GetCertificate,
		}
	}

	logger.Info("statnive-live starting",
		"listen", cfg.Server.Listen,
		"tls", tlsLoader != nil,
		"clickhouse", cfg.ClickHouse.Addr,
		"wal_dir", cfg.Ingest.WALDir,
	)

	g, gctx := errgroup.WithContext(rootCtx)

	// Pipeline is now a synchronous library — no goroutine to run.
	// Enrichment happens inline on the handler goroutine; durability
	// comes from groupSyncer (its loop runs internally, started by
	// NewGroupSyncer above and stopped by the deferred groupSyncer.Close).

	g.Go(func() error {
		consumer.Run(gctx)

		return nil
	})

	g.Go(func() error {
		if tlsLoader != nil {
			if listenErr := srv.ListenAndServeTLS("", ""); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
				return fmt.Errorf("https listen: %w", listenErr)
			}

			return nil
		}

		if listenErr := srv.ListenAndServe(); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			return fmt.Errorf("http listen: %w", listenErr)
		}

		return nil
	})

	if tlsLoader != nil {
		g.Go(func() error {
			return cert.NewExpiryWatcher(tlsLoader, auditLog, alertsSink, time.Now).Run(gctx)
		})
	}

	g.Go(func() error {
		runSIGHUP(gctx, logger, auditLog, alertsSink, tlsLoader, channelMapper, goalSnapshot, geoIP, cfg.Enrich.GeoIPBinPath)

		return nil
	})

	// Phase 8 alert probers — both no-op when alertsSink is nil.
	g.Go(func() error {
		return alerts.ProbeClickHouseLoop(gctx, alertsSink, store.Ping, 30*time.Second)
	})

	g.Go(func() error {
		return alerts.ProbeDiskFillLoop(gctx, alertsSink, "/var/lib/statnive-live", time.Minute)
	})

	g.Go(func() error {
		<-gctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("http shutdown", "err", err)
		}

		return nil
	})

	if waitErr := g.Wait(); waitErr != nil {
		return waitErr
	}

	logger.Info("statnive-live stopped")

	return nil
}

// newTLSLoader returns nil + a startup WARN when both cert paths are
// empty (HTTP-only mode for dev/local). Returns an error if exactly one
// path is set — partial config is almost certainly a misconfiguration.
func newTLSLoader(cfg appConfig, auditLog *audit.Logger, logger *slog.Logger) (*cert.Loader, error) {
	switch {
	case cfg.TLS.CertFile == "" && cfg.TLS.KeyFile == "":
		logger.Warn("TLS disabled: both tls.cert_file and tls.key_file are empty; serving HTTP")

		return nil, nil
	case cfg.TLS.CertFile == "" || cfg.TLS.KeyFile == "":
		return nil, errors.New("tls: cert_file and key_file must both be set or both be empty")
	}

	return cert.New(cfg.TLS.CertFile, cfg.TLS.KeyFile, auditLog)
}

// runSIGHUP fans SIGHUP out to every reload-aware subsystem: TLS cert
// reload, audit-log file reopen, the channel mapper's sources reload,
// the goals snapshot, and (Phase 8) the GeoIP BIN hot-swap. One signal
// handler avoids per-package signal.Notify calls that race on the same
// signal.
//
// Order is TLS → audit.Reopen → channels → goals → GeoIP. Each reload
// is independent: a failure in one does NOT short-circuit the others
// (cert-expiry refresh must never be blocked by a goals SQL hiccup).
//
//nolint:gocyclo // fan-out is linear; each branch is a one-reload check that can't usefully share code with the next
func runSIGHUP(
	ctx context.Context, logger *slog.Logger, auditLog *audit.Logger,
	alertsSink *alerts.Sink,
	tlsLoader *cert.Loader, mapper *enrich.ChannelMapper, goalSnap *goals.Snapshot,
	geoIP enrich.GeoIPEnricher, geoIPPath string,
) {
	ch := make(chan os.Signal, 1)

	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			logger.Info("SIGHUP received; reloading")

			if tlsLoader != nil {
				if err := tlsLoader.Reload(); err != nil {
					logger.Warn("tls reload failed", "err", err)
				}
			}

			if err := auditLog.Reopen(); err != nil {
				logger.Warn("audit log reopen failed", "err", err)
			}

			if err := alertsSink.Reopen(); err != nil {
				logger.Warn("alerts sink reopen failed", "err", err)
			}

			if err := mapper.Reload(); err != nil {
				logger.Warn("channel mapper reload failed", "err", err)
			}

			if goalSnap != nil {
				if err := goalSnap.Reload(ctx); err != nil {
					logger.Warn("goals reload failed", "err", err)
					auditLog.Event(ctx, audit.EventGoalsReloadFailed,
						slog.String("err", err.Error()),
					)
				} else {
					auditLog.Event(ctx, audit.EventGoalsReloadOK,
						slog.Int("count", goalSnap.Size()),
					)
				}
			}

			// GeoIP reload is a no-op on the noopGeoIP variant (when
			// no BIN path is configured); the Reload method handles
			// that case transparently. A failed reload keeps the old
			// handle active — emit the failure event but don't fall
			// back to the no-op enricher.
			if geoIP != nil && geoIPPath != "" {
				if err := geoIP.Reload(geoIPPath); err != nil {
					logger.Warn("geoip reload failed", "err", err, "path", geoIPPath)
					auditLog.Event(ctx, audit.EventGeoIPReloadFailed,
						slog.String("err", err.Error()),
						slog.String("path", geoIPPath),
					)
				} else {
					auditLog.Event(ctx, audit.EventGeoIPReloaded,
						slog.String("path", geoIPPath),
					)
				}
			}
		}
	}
}

// replayWAL drains every persisted event from the WAL into ClickHouse,
// then truncates the WAL through the last replayed index. Called once
// at startup before the consumer reads from the live pipeline.
//
// Snapshot semantics: read LastIndex BEFORE replay; ack to that index
// AFTER. The pipeline isn't started yet, so no new events arrive
// during replay. Batches into chunks of replayBatchSize so a multi-GB
// WAL doesn't blow up memory or send a single oversized INSERT.
func replayWAL(ctx context.Context, wal *ingest.WALWriter, store *storage.ClickHouseStore, logger *slog.Logger) error {
	const replayBatchSize = 1000

	snapshotIdx := wal.CurrentIndex()
	if snapshotIdx == 0 {
		return nil
	}

	batch := make([]ingest.EnrichedEvent, 0, replayBatchSize)
	total := 0

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}

		if err := store.InsertBatch(ctx, batch); err != nil {
			return fmt.Errorf("replay insert: %w", err)
		}

		total += len(batch)
		batch = batch[:0]

		return nil
	}

	err := wal.Replay(func(ev ingest.EnrichedEvent) error {
		batch = append(batch, ev)

		if len(batch) >= replayBatchSize {
			return flush()
		}

		return nil
	})
	if err != nil {
		return err
	}

	if err := flush(); err != nil {
		return err
	}

	if total > 0 {
		if ackErr := wal.Ack(snapshotIdx); ackErr != nil {
			logger.Warn("wal replay ack failed", "err", ackErr, "through", snapshotIdx)
		}

		logger.Info("wal replay complete", "events", total, "through_idx", snapshotIdx)
	}

	return nil
}

// buildAPITokens assembles the static API-token list passed to the
// APITokenMiddleware. Sources:
//  1. cfg.Auth.APITokens — operators paste {token_hash_sha256, site_id,
//     label, role} triplets into config.
//  2. STATNIVE_API_TOKENS env var — CI/smoke convenience,
//     "label1:rawtoken1,label2:rawtoken2". Each raw token is hashed at
//     boot and added to the list. Raw tokens never land on disk.
//  3. cfg.Dashboard.BearerToken — legacy Phase 3b config key; if set,
//     promoted as an implicit `bearer-legacy` entry so the Phase 5a-smoke
//     harness continues to work while operators migrate.
func buildAPITokens(cfg appConfig) []auth.APIToken {
	out := make([]auth.APIToken, 0, len(cfg.Auth.APITokens)+4)

	for _, t := range cfg.Auth.APITokens {
		out = append(out, auth.APIToken{
			TokenHashHex: t.TokenHashHex,
			SiteID:       t.SiteID,
			Label:        t.Label,
			Role:         auth.Role(t.Role),
		})
	}

	if env := os.Getenv("STATNIVE_API_TOKENS"); env != "" {
		for _, pair := range strings.Split(env, ",") {
			label, raw, ok := strings.Cut(pair, ":")
			if !ok || raw == "" {
				continue
			}

			sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))

			out = append(out, auth.APIToken{
				TokenHashHex: hex.EncodeToString(sum[:]),
				SiteID:       cfg.Auth.DefaultSiteID,
				Label:        strings.TrimSpace(label),
				Role:         auth.RoleAPI,
			})
		}
	}

	if cfg.Dashboard.BearerToken != "" {
		sum := sha256.Sum256([]byte(cfg.Dashboard.BearerToken))
		out = append(out, auth.APIToken{
			TokenHashHex: hex.EncodeToString(sum[:]),
			SiteID:       cfg.Auth.DefaultSiteID,
			Label:        "bearer-legacy",
			Role:         auth.RoleAPI,
		})
	}

	return out
}

// readBuildInfo pulls version + git SHA + Go version out of
// runtime/debug's build record. Works whether the binary was produced
// via `go build` (vcs.revision populated from the git tree) or
// `go install` from a tagged module (main.Version populated from the
// module's tag). In dev (GOFLAGS= / no vcs info), all three return
// empty strings — /api/about still serves.
func readBuildInfo() about.BuildInfo {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return about.BuildInfo{}
	}

	out := about.BuildInfo{
		Version:   info.Main.Version,
		GoVersion: info.GoVersion,
	}

	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			out.GitSHA = s.Value[:7]
		}
	}

	return out
}

// appConfig is the typed view of statnive-live.yaml.
type appConfig struct {
	MasterSecretPath string

	Server struct {
		Listen       string
		ReadTimeout  time.Duration
		WriteTimeout time.Duration
	}
	ClickHouse struct {
		Addr     string
		Database string
		Username string
		Password string
		Cluster  string
	}
	Ingest struct {
		WALDir                          string
		WALMaxBytes                     int64
		BatchRows                       int
		BatchInterval                   time.Duration
		BatchMaxBytes                   int
		MaxPageviewsPerMinutePerVisitor int
	}
	Enrich struct {
		SourcesPath  string
		GeoIPBinPath string
	}
	TLS struct {
		CertFile string
		KeyFile  string
	}
	Audit struct {
		Path string
	}
	Alerts struct {
		// SinkPath is /var/log/statnive-live/alerts.jsonl by convention;
		// empty disables the file sink entirely (the emitter becomes a
		// no-op). Phase 8 adds this as the backend feed for
		// Phase 6-polish-5's Notice UI.
		SinkPath string
		// HostTag populates the `host` field in every alert, e.g.
		// "cx32-staging". Optional; leave empty to omit the field.
		HostTag string
	}
	RateLimit struct {
		RequestsPerMinute int
		// AllowlistedIPs are exempt from the per-IP rate-limit ladder.
		// Used for load testing only — set the generator IP here so the
		// 100 req/s/IP fallback doesn't cap a synthetic ramp. Bypass is
		// limited to the rate limiter; UA / hostname / payload / WAL
		// gates still apply.
		AllowlistedIPs []string
	}
	Metrics struct {
		// Token gates the /metrics endpoint via Authorization: Bearer.
		// Empty disables the endpoint entirely (404). Set via
		// STATNIVE_METRICS_TOKEN systemd Environment= drop-in.
		Token string
	}
	Dashboard struct {
		// BearerToken is the legacy Phase 3b pre-shared secret. Kept
		// for backward-compat: when set it is promoted into Auth.APITokens
		// as a single implicit ci-smoke entry so the Phase 5a-smoke
		// harness continues to work while operators migrate.
		BearerToken string
		// SPAEnabled gates the embedded Preact dashboard at /app/*.
		// Phase 2b (this file) now supplies session + RBAC auth, so the
		// hard gate is lifted — operators may enable SPA in production.
		SPAEnabled bool
	}
	Auth struct {
		Session struct {
			TTL        time.Duration
			CookieName string
			Secure     bool
			SameSite   string // "lax" only; other values rejected in v1
			CacheTTL   time.Duration
		}
		BcryptCost     int
		LoginRateLimit struct {
			Requests int
			Window   time.Duration
		}
		Lockout struct {
			Fails   int
			Decay   time.Duration
			Lockout time.Duration
		}
		APITokens []struct {
			TokenHashHex string
			SiteID       uint32
			Label        string
			Role         string
		}
		Bootstrap struct {
			SiteID   uint32
			Username string
		}
		DefaultSiteID uint32
		DemoBanner    string
	}
	Consent struct {
		// Required gates the _statnive cookie + user_id hashing behind
		// an explicit X-Statnive-Consent: given header. Default true on
		// the SaaS binary; self-hosted Iran tier flips to false.
		Required bool
		// Note: respect_gpc + respect_dnt are now per-site columns in
		// statnive.sites (migration 006), not global cfg flags. The
		// admin UI at /admin/sites toggles them per tenant. PR D2.
	}
}

// loadConfig parses CLI flags + env vars to find the config file path,
// then defers to loadConfigFromPath. Splitting the two means tests can
// drive loadConfigFromPath directly without flag-parse side effects.
func loadConfig() (appConfig, error) {
	fs := flag.NewFlagSet("statnive-live", flag.ContinueOnError)

	var configFile string

	fs.StringVar(&configFile, "c", "", "path to YAML config file")
	fs.StringVar(&configFile, "config", "", "path to YAML config file (long form)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return appConfig{}, fmt.Errorf("parse flags: %w", err)
	}

	if configFile == "" {
		configFile = os.Getenv(configFileEnv)
	}

	return loadConfigFromPath(configFile)
}

// loadConfigFromPath builds the typed appConfig. When configFile is
// non-empty, viper opens that exact path; a missing-file os.PathError
// is fatal so an operator-supplied path can't be silently ignored
// (LEARN.md Lesson 6). When configFile is empty, viper searches
// ./config and .; a search miss returns ConfigFileNotFoundError which
// becomes a stderr warning + defaults (back-compat for dev).
//
//nolint:funlen // YAML loader with 15+ distinct sections; splitting fragments the config surface
func loadConfigFromPath(configFile string) (appConfig, error) {
	v := viper.New()
	if configFile != "" {
		v.SetConfigFile(configFile)
		// SetConfigFile auto-detects type from extension; .yaml.example
		// would parse as "example" (unsupported). Pin the type.
		v.SetConfigType("yaml")
	} else {
		v.SetConfigName("statnive-live")
		v.SetConfigType("yaml")
		v.AddConfigPath("./config")
		v.AddConfigPath(".")
	}

	v.SetEnvPrefix("STATNIVE")
	v.AutomaticEnv()
	// Map nested config keys to env vars: clickhouse.addr → STATNIVE_CLICKHOUSE_ADDR.
	// Without this, AutomaticEnv silently misses every dotted key.
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	v.SetDefault("master_secret_path", "./config/master.key")
	v.SetDefault("server.listen", "127.0.0.1:8080")
	v.SetDefault("server.read_timeout", "5s")
	v.SetDefault("server.write_timeout", "5s")
	v.SetDefault("clickhouse.addr", "127.0.0.1:9000")
	v.SetDefault("clickhouse.database", "statnive")
	v.SetDefault("clickhouse.username", "default")
	v.SetDefault("clickhouse.password", "")
	v.SetDefault("clickhouse.cluster", "")
	v.SetDefault("ingest.wal_dir", "./wal")
	v.SetDefault("ingest.wal_max_bytes", int64(10*1024*1024*1024))
	v.SetDefault("ingest.batch_rows", 1000)
	v.SetDefault("ingest.batch_interval", "500ms")
	v.SetDefault("ingest.batch_max_bytes", 10*1024*1024)
	v.SetDefault("ingest.max_pageviews_per_minute_per_visitor", 500)
	v.SetDefault("enrich.sources_path", "./config/sources.yaml")
	v.SetDefault("enrich.geoip_bin_path", "")
	v.SetDefault("tls.cert_file", "")
	v.SetDefault("tls.key_file", "")
	v.SetDefault("audit.path", "./audit.jsonl")
	v.SetDefault("alerts.sink_path", "")
	v.SetDefault("alerts.host_tag", "")
	v.SetDefault("ratelimit.requests_per_minute", 6000)
	v.SetDefault("dashboard.bearer_token", "")
	v.SetDefault("dashboard.spa_enabled", false)

	// Consent posture (CLAUDE.md Privacy Rules 5 + 9).
	// `consent.required` stays a global flag (cookie + identity gate is
	// jurisdiction-wide, not per-site). The respect_gpc + respect_dnt
	// flags moved to per-site columns in statnive.sites (migration 006,
	// PR D2) so multi-tenant operators can serve EU + non-EU customers
	// from the same binary without re-editing config.
	v.SetDefault("consent.required", true)

	// Auth (Phase 2b). Secure defaults: 14-day cookie, SameSite=Lax,
	// bcrypt cost 12, 10 login attempts / min / IP, 10 fails / 15 min →
	// 5 min per-email lockout. Session-cache TTL 60 s.
	v.SetDefault("auth.session.ttl", "336h")
	v.SetDefault("auth.session.cookie_name", "statnive_session")
	v.SetDefault("auth.session.secure", true)
	v.SetDefault("auth.session.same_site", "lax")
	v.SetDefault("auth.session.cache_ttl", "60s")
	v.SetDefault("auth.bcrypt_cost", 12)
	v.SetDefault("auth.login_rate_limit.requests", 10)
	v.SetDefault("auth.login_rate_limit.window", "1m")
	v.SetDefault("auth.lockout.fails", 10)
	v.SetDefault("auth.lockout.decay", "15m")
	v.SetDefault("auth.lockout.lockout", "5m")
	v.SetDefault("auth.bootstrap.site_id", 1)
	v.SetDefault("auth.bootstrap.username", "admin")
	v.SetDefault("auth.default_site_id", 1)
	v.SetDefault("auth.demo_banner", "")

	if readErr := v.ReadInConfig(); readErr != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(readErr, &notFound) {
			// Any error other than search-mode miss is fatal: parse
			// errors, permission errors, or missing-explicit-file
			// (os.PathError, the case when SetConfigFile pointed at a
			// path that doesn't exist on disk).
			return appConfig{}, fmt.Errorf("read config: %w", readErr)
		}

		fmt.Fprintln(os.Stderr, "statnive-live: WARN: no config file found at ./config/statnive-live.yaml or ./statnive-live.yaml; using defaults + STATNIVE_* env")
	}

	var cfg appConfig

	cfg.MasterSecretPath = v.GetString("master_secret_path")

	cfg.Server.Listen = v.GetString("server.listen")
	cfg.Server.ReadTimeout = v.GetDuration("server.read_timeout")
	cfg.Server.WriteTimeout = v.GetDuration("server.write_timeout")

	cfg.ClickHouse.Addr = v.GetString("clickhouse.addr")
	cfg.ClickHouse.Database = v.GetString("clickhouse.database")
	cfg.ClickHouse.Username = v.GetString("clickhouse.username")
	cfg.ClickHouse.Password = v.GetString("clickhouse.password")
	cfg.ClickHouse.Cluster = v.GetString("clickhouse.cluster")

	cfg.Ingest.WALDir = v.GetString("ingest.wal_dir")
	cfg.Ingest.WALMaxBytes = v.GetInt64("ingest.wal_max_bytes")
	cfg.Ingest.BatchRows = v.GetInt("ingest.batch_rows")
	cfg.Ingest.BatchInterval = v.GetDuration("ingest.batch_interval")
	cfg.Ingest.BatchMaxBytes = v.GetInt("ingest.batch_max_bytes")
	cfg.Ingest.MaxPageviewsPerMinutePerVisitor = v.GetInt("ingest.max_pageviews_per_minute_per_visitor")

	cfg.Enrich.SourcesPath = v.GetString("enrich.sources_path")
	cfg.Enrich.GeoIPBinPath = v.GetString("enrich.geoip_bin_path")

	cfg.TLS.CertFile = v.GetString("tls.cert_file")
	cfg.TLS.KeyFile = v.GetString("tls.key_file")

	cfg.Audit.Path = v.GetString("audit.path")
	cfg.Alerts.SinkPath = v.GetString("alerts.sink_path")
	cfg.Alerts.HostTag = v.GetString("alerts.host_tag")

	cfg.RateLimit.RequestsPerMinute = v.GetInt("ratelimit.requests_per_minute")
	cfg.RateLimit.AllowlistedIPs = v.GetStringSlice("ratelimit.allowlisted_ips")
	cfg.Metrics.Token = v.GetString("metrics.token")

	cfg.Dashboard.BearerToken = v.GetString("dashboard.bearer_token")
	cfg.Dashboard.SPAEnabled = v.GetBool("dashboard.spa_enabled")

	cfg.Consent.Required = v.GetBool("consent.required")

	cfg.Auth.Session.TTL = v.GetDuration("auth.session.ttl")
	cfg.Auth.Session.CookieName = v.GetString("auth.session.cookie_name")
	cfg.Auth.Session.Secure = v.GetBool("auth.session.secure")
	cfg.Auth.Session.SameSite = strings.ToLower(v.GetString("auth.session.same_site"))
	cfg.Auth.Session.CacheTTL = v.GetDuration("auth.session.cache_ttl")
	cfg.Auth.BcryptCost = v.GetInt("auth.bcrypt_cost")
	cfg.Auth.LoginRateLimit.Requests = v.GetInt("auth.login_rate_limit.requests")
	cfg.Auth.LoginRateLimit.Window = v.GetDuration("auth.login_rate_limit.window")
	cfg.Auth.Lockout.Fails = v.GetInt("auth.lockout.fails")
	cfg.Auth.Lockout.Decay = v.GetDuration("auth.lockout.decay")
	cfg.Auth.Lockout.Lockout = v.GetDuration("auth.lockout.lockout")
	cfg.Auth.Bootstrap.SiteID = v.GetUint32("auth.bootstrap.site_id")
	cfg.Auth.Bootstrap.Username = v.GetString("auth.bootstrap.username")
	cfg.Auth.DefaultSiteID = v.GetUint32("auth.default_site_id")
	cfg.Auth.DemoBanner = v.GetString("auth.demo_banner")

	// auth.api_tokens is a list of {token_hash, site_id, label, role}
	// entries. Viper's UnmarshalKey handles the list-of-maps shape.
	_ = v.UnmarshalKey("auth.api_tokens", &cfg.Auth.APITokens)

	return cfg, nil
}

// walFillThresholds matches wal-durability-review item #6 + PLAN.md:159
// (503 at 0.80). 0.90 / 0.95 add two critical bands so ops sees
// escalation before cap-fire drops oldest segments.
var walFillThresholds = [3]float64{0.80, 0.90, 0.95}

// walFillAlertEmitter wires an alerts.Sink to the BackpressureMiddleware's
// per-TTL sample callback. The BandTracker debounces so the sink sees
// one event per real crossing — not one per sample.
func walFillAlertEmitter(sink *alerts.Sink) func(ratio float64) {
	if sink == nil {
		return nil
	}

	var tracker alerts.BandTracker

	return func(ratio float64) {
		band, sev := alerts.ClassifyRatio(ratio, walFillThresholds)

		t := tracker.Observe(band)
		if !t.Entered && !t.Exited {
			return
		}

		resolved := t.Exited && band == 0

		sink.Emit(context.Background(), alerts.NameWALHighFillRatio, sev, resolved,
			slog.Float64("value", ratio),
			slog.Int("band", int(band)),
		)
	}
}
