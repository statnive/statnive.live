// statnive-live — Go single binary + ClickHouse analytics platform.
// Phase 2a build: TLS 1.3 with manual PEM + SIGHUP reload, NAT-aware
// httprate rate limiting on /api/event, JSONL audit log file sink with
// SIGHUP-aware reopen for logrotate. Auth + RBAC + systemd hardening land
// in subsequent slices per PLAN.md.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/cert"
	"github.com/statnive/statnive.live/internal/config"
	"github.com/statnive/statnive.live/internal/dashboard"
	"github.com/statnive/statnive.live/internal/enrich"
	"github.com/statnive/statnive.live/internal/health"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/ratelimit"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
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
	}, logger)
	if err != nil {
		return fmt.Errorf("wal: %w", err)
	}
	defer func() { _ = wal.Close() }()

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

	pipeline := enrich.NewPipeline(enrich.Deps{
		Salt:    saltMgr,
		Bloom:   bloom,
		GeoIP:   geoIP,
		UA:      enrich.NewUAParser(),
		Bot:     enrich.NewBotDetector(logger),
		Channel: channelMapper,
		Logger:  logger,
	})

	consumer := ingest.NewConsumer(pipeline.Out(), wal, store, ingest.ConsumerConfig{
		BatchRows:     cfg.Ingest.BatchRows,
		BatchInterval: cfg.Ingest.BatchInterval,
		BatchMaxBytes: cfg.Ingest.BatchMaxBytes,
	}, logger)

	registry := sites.New(store.Conn())

	cachedStore := storage.NewCachedStore(storage.NewClickhouseQueryStore(store), dashboardCacheCapacity)

	rateLimitMW, err := ratelimit.Middleware(cfg.RateLimit.RequestsPerMinute, time.Minute, auditLog)
	if err != nil {
		return fmt.Errorf("ratelimit: %w", err)
	}

	dashboardAuthMW := dashboard.BearerTokenMiddleware(cfg.Dashboard.BearerToken, auditLog)

	router := chi.NewRouter()

	// /api/event runs the fast-reject gate BEFORE the rate limiter so
	// prefetches + obvious-bot UAs don't burn per-IP budget. Dashboard
	// routes share the rate limiter (so abusive polling can't drain
	// ClickHouse) but skip fast-reject (operators don't send tracker
	// prefetches). /healthz stays unconditionally reachable for probes.
	router.Group(func(r chi.Router) {
		r.Use(ingest.FastRejectMiddleware(auditLog))
		r.Use(rateLimitMW)
		r.Method(http.MethodPost, "/api/event", ingest.NewHandler(ingest.HandlerConfig{
			Pipeline: pipeline,
			Sites:    registry,
			Audit:    auditLog,
			Logger:   logger,
		}))
	})

	router.Group(func(r chi.Router) {
		r.Use(rateLimitMW)
		r.Use(dashboardAuthMW)
		dashboard.Mount(r, dashboard.Deps{
			Store:  cachedStore,
			Audit:  auditLog,
			Logger: logger,
		})
	})

	router.Method(http.MethodGet, "/healthz", health.Handler(health.Reporter{
		Store: store,
		WAL:   wal,
		Start: time.Now(),
	}))

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

	g.Go(func() error {
		return pipeline.Run(gctx)
	})

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
		WALDir        string
		WALMaxBytes   int64
		BatchRows     int
		BatchInterval time.Duration
		BatchMaxBytes int
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
		// BearerToken is the pre-shared secret required on every
		// dashboard request when set. Empty = no auth (dev only).
		// Replaced by Phase 2b's bcrypt + sessions + RBAC.
		BearerToken string
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
	v.SetDefault("enrich.sources_path", "./config/sources.yaml")
	v.SetDefault("enrich.geoip_bin_path", "")
	v.SetDefault("tls.cert_file", "")
	v.SetDefault("tls.key_file", "")
	v.SetDefault("audit.path", "./audit.jsonl")
	v.SetDefault("ratelimit.requests_per_minute", 6000)
	v.SetDefault("dashboard.bearer_token", "")

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

	cfg.Enrich.SourcesPath = v.GetString("enrich.sources_path")
	cfg.Enrich.GeoIPBinPath = v.GetString("enrich.geoip_bin_path")

	cfg.TLS.CertFile = v.GetString("tls.cert_file")
	cfg.TLS.KeyFile = v.GetString("tls.key_file")

	cfg.Audit.Path = v.GetString("audit.path")

	cfg.RateLimit.RequestsPerMinute = v.GetInt("ratelimit.requests_per_minute")

	cfg.Dashboard.BearerToken = v.GetString("dashboard.bearer_token")

	return cfg, nil
}
