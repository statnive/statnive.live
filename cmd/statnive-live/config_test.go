package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// clearStatniveEnvs removes every STATNIVE_* env var for the duration
// of a test so AutomaticEnv doesn't override the example's values from
// the developer's shell. Restored on cleanup.
//
// Uses os.Unsetenv (not t.Setenv) because viper's AutomaticEnv treats
// "" as "set to empty string" and clobbers SetDefault values; only an
// unset env reliably falls through to defaults. t.Setenv has no
// unset-for-the-duration mode, so manual save/restore is required.
func clearStatniveEnvs(t *testing.T) {
	t.Helper()

	saved := map[string]string{}

	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "STATNIVE_") {
			continue
		}

		i := strings.IndexByte(e, '=')
		if i <= 0 {
			continue
		}

		k := e[:i]
		saved[k] = os.Getenv(k)
		_ = os.Unsetenv(k)
	}

	t.Cleanup(func() {
		for k, v := range saved {
			_ = os.Setenv(k, v) //nolint:usetesting // t.Setenv inside Cleanup races with the save/restore; os.Setenv is the only way to restore
		}
	})
}

// examplePath resolves config/statnive-live.yaml.example relative to
// this test file, independent of the test's CWD.
func examplePath(t *testing.T) string {
	t.Helper()

	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}

	return filepath.Join(filepath.Dir(here), "..", "..", "config", "statnive-live.yaml.example")
}

// TestLoadConfig_ExampleParity round-trips config/statnive-live.yaml.example
// through loadConfigFromPath and asserts every key in the example
// populates a non-zero appConfig field. A zero value here means the
// example shipped a key that the binary's loadConfig doesn't read —
// LEARN.md Lesson 4's bug class. PLAN.md Verification §59.
//
// Cannot use t.Parallel() — clearStatniveEnvs mutates process-global
// env, which would race with parallel sibling tests.
func TestLoadConfig_ExampleParity(t *testing.T) { //nolint:paralleltest // mutates process env
	clearStatniveEnvs(t)

	path := examplePath(t)

	cfg, err := loadConfigFromPath(path)
	if err != nil {
		t.Fatalf("loadConfigFromPath(%q): %v", path, err)
	}

	cases := []struct {
		key string
		ok  bool
	}{
		{"master_secret_path", cfg.MasterSecretPath != ""},
		{"server.listen", cfg.Server.Listen != ""},
		{"server.read_timeout", cfg.Server.ReadTimeout > 0},
		{"server.write_timeout", cfg.Server.WriteTimeout > 0},
		{"tls.cert_file", cfg.TLS.CertFile != ""},
		{"tls.key_file", cfg.TLS.KeyFile != ""},
		{"clickhouse.addr", cfg.ClickHouse.Addr != ""},
		{"clickhouse.database", cfg.ClickHouse.Database != ""},
		{"clickhouse.username", cfg.ClickHouse.Username != ""},
		{"ingest.wal_dir", cfg.Ingest.WALDir != ""},
		{"ingest.wal_max_bytes", cfg.Ingest.WALMaxBytes > 0},
		{"ingest.batch_rows", cfg.Ingest.BatchRows > 0},
		{"ingest.batch_interval", cfg.Ingest.BatchInterval > 0},
		{"ingest.batch_max_bytes", cfg.Ingest.BatchMaxBytes > 0},
		{"ingest.max_pageviews_per_minute_per_visitor", cfg.Ingest.MaxPageviewsPerMinutePerVisitor > 0},
		{"enrich.sources_path", cfg.Enrich.SourcesPath != ""},
		{"enrich.geoip_bin_path", cfg.Enrich.GeoIPBinPath != ""},
		{"audit.path", cfg.Audit.Path != ""},
		{"alerts.sink_path", cfg.Alerts.SinkPath != ""},
		{"ratelimit.requests_per_minute", cfg.RateLimit.RequestsPerMinute > 0},
		{"dashboard.spa_enabled", cfg.Dashboard.SPAEnabled},
		{"auth.session.ttl", cfg.Auth.Session.TTL > 0},
		{"auth.session.cookie_name", cfg.Auth.Session.CookieName != ""},
		{"auth.session.secure", cfg.Auth.Session.Secure},
		{"auth.session.same_site", cfg.Auth.Session.SameSite != ""},
		{"auth.session.cache_ttl", cfg.Auth.Session.CacheTTL > 0},
		{"auth.bcrypt_cost", cfg.Auth.BcryptCost > 0},
		{"auth.login_rate_limit.requests", cfg.Auth.LoginRateLimit.Requests > 0},
		{"auth.login_rate_limit.window", cfg.Auth.LoginRateLimit.Window > 0},
		{"auth.lockout.fails", cfg.Auth.Lockout.Fails > 0},
		{"auth.lockout.decay", cfg.Auth.Lockout.Decay > 0},
		{"auth.lockout.lockout", cfg.Auth.Lockout.Lockout > 0},
		{"auth.bootstrap.site_id", cfg.Auth.Bootstrap.SiteID > 0},
		{"auth.bootstrap.username", cfg.Auth.Bootstrap.Username != ""},
		{"auth.default_site_id", cfg.Auth.DefaultSiteID > 0},
	}

	for _, c := range cases {
		if !c.ok {
			t.Errorf("%s: zero value after loading example — schema drift between example and binary's loadConfig", c.key)
		}
	}
}

// TestLoadConfig_ExplicitMissingFile asserts that pointing at a
// non-existent file (via -c, --config, or STATNIVE_CONFIG_FILE) is
// fatal — viper returns os.PathError which falls to the non-search
// branch and surfaces as a hard error, so an operator-supplied path
// can't be silently ignored. Closes LEARN.md Lesson 6.
func TestLoadConfig_ExplicitMissingFile(t *testing.T) { //nolint:paralleltest // mutates process env
	clearStatniveEnvs(t)

	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	_, err := loadConfigFromPath(missing)
	if err == nil {
		t.Fatal("expected error on explicit missing config file; got nil")
	}
}

// TestLoadConfig_ImplicitMissingFile asserts that no config file at
// the default search paths is non-fatal — defaults still populate so
// `go run ./cmd/statnive-live` in a dev tree works without a config
// file. The warning goes to stderr (visible in journald).
func TestLoadConfig_ImplicitMissingFile(t *testing.T) { //nolint:paralleltest // mutates process env
	clearStatniveEnvs(t)

	cfg, err := loadConfigFromPath("")
	if err != nil {
		t.Fatalf("expected nil error on implicit miss; got %v", err)
	}

	if cfg.Server.Listen == "" {
		t.Error("Server.Listen empty after default fallback")
	}
}
