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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/cert"
	"github.com/statnive/statnive.live/internal/config"
	"github.com/statnive/statnive.live/internal/dashboard"
	"github.com/statnive/statnive.live/internal/dashboard/spa"
	"github.com/statnive/statnive.live/internal/enrich"
	"github.com/statnive/statnive.live/internal/health"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/ingest"
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
	bloomCapacity   = 10_000_000
	bloomFPRate     = 0.001
	masterSecretEnv = "STATNIVE_MASTER_SECRET"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "statnive-live: %v\n", err)
		os.Exit(1)
	}
}

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

	pipeline := enrich.NewPipeline(enrich.Deps{
		Salt:    saltMgr,
		Bloom:   bloom,
		GeoIP:   geoIP,
		UA:      enrich.NewUAParser(),
		Bot:     enrich.NewBotDetector(logger),
		Channel: channelMapper,
		Burst:   burstGuard,
		Audit:   auditLog,
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

	rateLimitMW, err := ratelimit.Middleware(cfg.RateLimit.RequestsPerMinute, time.Minute, auditLog)
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
		auditLog,
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
		r.Use(ingest.FastRejectMiddleware(auditLog))
		r.Use(rateLimitMW)
		// Back-pressure gate sits AFTER rate-limit (abusive clients still
		// hit 429 first) but BEFORE the handler (so a degraded WAL
		// doesn't burn enrichment + fsync budget on events destined for
		// 503). wal-durability-review item #6.
		r.Use(ingest.BackpressureMiddleware(wal, ingest.BackpressureConfig{}))
		r.Method(http.MethodPost, "/api/event", ingest.NewHandler(ingest.HandlerConfig{
			Pipeline:     pipeline,
			WAL:          groupSyncer,
			Sites:        registry,
			MasterSecret: masterSecret,
			Audit:        auditLog,
			Logger:       logger,
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

	// Admin CRUD reserved slot — Phase 3c fills this in. Stays admin-
	// only so viewers can't probe it for 501s during Phase 2b.
	router.Group(func(r chi.Router) {
		r.Use(rateLimitMW)
		r.Use(sessionMW)
		r.Use(apiTokenMW)
		r.Use(requireAuthed)
		r.Use(auth.RequireRole(auditLog, auth.RoleAdmin))
		// Placeholder — Phase 3c adds /api/admin/users, /api/admin/goals, etc.
	})

	router.Method(http.MethodGet, "/healthz", health.Handler(health.Reporter{
		Store:     store,
		WAL:       wal,
		WALSyncer: groupSyncer,
		Start:     time.Now(),
	}))

	// First-party tracker — bytes embedded via go:embed in internal/tracker.
	// Sits outside the dashboard auth + rate-limit groups; serves a static
	// blob that's safe to hand back unauthenticated under any traffic.
	router.Method(http.MethodGet, "/tracker.js", tracker.Handler())

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
		router.Method(http.MethodGet, "/", http.RedirectHandler("/app/", http.StatusFound))

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
			return cert.NewExpiryWatcher(tlsLoader, auditLog, time.Now).Run(gctx)
		})
	}

	g.Go(func() error {
		runSIGHUP(gctx, logger, auditLog, tlsLoader, channelMapper)

		return nil
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
		return nil, fmt.Errorf("tls: cert_file and key_file must both be set or both be empty")
	}

	return cert.New(cfg.TLS.CertFile, cfg.TLS.KeyFile, auditLog)
}

// runSIGHUP fans SIGHUP out to every reload-aware subsystem: TLS cert
// reload, audit-log file reopen, and the channel mapper's sources reload.
// One signal handler avoids per-package signal.Notify calls that race on
// the same signal.
func runSIGHUP(ctx context.Context, logger *slog.Logger, auditLog *audit.Logger, tlsLoader *cert.Loader, mapper *enrich.ChannelMapper) {
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

			if err := mapper.Reload(); err != nil {
				logger.Warn("channel mapper reload failed", "err", err)
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
	RateLimit struct {
		RequestsPerMinute int
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
		BcryptCost      int
		LoginRateLimit  struct {
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
}

func loadConfig() (appConfig, error) {
	v := viper.New()
	v.SetConfigName("statnive-live")
	v.SetConfigType("yaml")
	v.AddConfigPath("./config")
	v.AddConfigPath(".")
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
	v.SetDefault("ratelimit.requests_per_minute", 6000)
	v.SetDefault("dashboard.bearer_token", "")

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
			return appConfig{}, fmt.Errorf("read config: %w", readErr)
		}
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

	cfg.RateLimit.RequestsPerMinute = v.GetInt("ratelimit.requests_per_minute")

	cfg.Dashboard.BearerToken = v.GetString("dashboard.bearer_token")
	cfg.Dashboard.SPAEnabled = v.GetBool("dashboard.spa_enabled")

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
	cfg.Auth.Bootstrap.SiteID = uint32(v.GetUint32("auth.bootstrap.site_id"))
	cfg.Auth.Bootstrap.Username = v.GetString("auth.bootstrap.username")
	cfg.Auth.DefaultSiteID = uint32(v.GetUint32("auth.default_site_id"))
	cfg.Auth.DemoBanner = v.GetString("auth.demo_banner")

	// auth.api_tokens is a list of {token_hash, site_id, label, role}
	// entries. Viper's UnmarshalKey handles the list-of-maps shape.
	_ = v.UnmarshalKey("auth.api_tokens", &cfg.Auth.APITokens)

	return cfg, nil
}
