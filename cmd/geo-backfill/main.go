// Command geo-backfill bulk-loads historical events_raw rows into the
// daily_geo rollup created by migration 019. Designed to be run by the
// operator AFTER the server-binary deploy is verified stable — never
// inline in the migration (production-safety contract in PLAN.md
// v1.1-geo § Migration safety).
//
// Per-day chunking caps each INSERT ... SELECT's working set; an
// idempotency check skips chunks whose daily_geo.views already match
// the events_raw count for that (site_id, day). The HLL columns are
// safe to re-insert (uniqCombined64State of the same visitor_hash
// merges to the same unique), but the SUM columns are not, so the
// skip is required for safe re-runs.
//
// Connection settings come from the same YAML config the main binary
// reads — operators run this with --config /etc/statnive-live/config.yaml
// so credentials don't leak into shell history.
//
// Usage:
//
//	geo-backfill --config /etc/statnive-live/config.yaml \
//	             --from 2025-01-01 \
//	             --to   2026-05-31
//
// Exit code 0 on success; 1 on any chunk failure (the operator can
// re-run with --from <last-success+1> to resume).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/spf13/viper"

	"github.com/statnive/statnive.live/internal/storage"
)

const dateLayout = "2006-01-02"

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "geo-backfill:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath string
		fromStr    string
		toStr      string
		dryRun     bool
		skipCheck  bool
	)

	flag.StringVar(&configPath, "config", "/etc/statnive-live/config.yaml",
		"Path to statnive-live config YAML (same file the main binary reads).")
	flag.StringVar(&fromStr, "from", "",
		"Inclusive start date YYYY-MM-DD. Required: there's no sensible default for the earliest events_raw row.")
	flag.StringVar(&toStr, "to", time.Now().UTC().Format(dateLayout),
		"Inclusive end date YYYY-MM-DD. Default: today UTC.")
	flag.BoolVar(&dryRun, "dry-run", false,
		"Print the per-day INSERT statements without executing them.")
	flag.BoolVar(&skipCheck, "skip-idempotency-check", false,
		"Force every chunk to re-INSERT even if daily_geo.views already matches. Dangerous on additive columns.")
	flag.Parse()

	if fromStr == "" {
		return errors.New("--from is required (YYYY-MM-DD)")
	}

	from, err := time.Parse(dateLayout, fromStr)
	if err != nil {
		return fmt.Errorf("parse --from: %w", err)
	}

	to, err := time.Parse(dateLayout, toStr)
	if err != nil {
		return fmt.Errorf("parse --to: %w", err)
	}

	if to.Before(from) {
		return fmt.Errorf("--to %s precedes --from %s", toStr, fromStr)
	}

	chCfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := storage.NewClickHouseStore(ctx, chCfg, logger)
	if err != nil {
		return fmt.Errorf("clickhouse: %w", err)
	}

	defer func() { _ = store.Close() }()

	conn := store.Conn()
	database := chCfg.Database

	logger.Info("geo-backfill starting", "from", fromStr, "to", toStr, "database", database, "dry_run", dryRun)

	if !dryRun {
		if err := assertDailyGeoExists(ctx, conn, database); err != nil {
			return err
		}
	}

	job := backfillJob{
		conn:     conn,
		database: database,
		from:     from,
		to:       to,
		dryRun:   dryRun,
		skipChk:  skipCheck,
		logger:   logger,
	}

	totals, err := job.loop(ctx)
	if err != nil {
		return err
	}

	logger.Info("geo-backfill done", "days_total", totals.total, "days_inserted", totals.inserted, "days_skipped", totals.skipped)

	return nil
}

// backfillJob is the per-invocation immutable context (connection, date
// range, behavior flags, logger). Holding it as a value reduces the
// per-day function signature from 8 parameters to 1.
type backfillJob struct {
	conn     driver.Conn
	database string
	from, to time.Time
	dryRun   bool
	skipChk  bool
	logger   *slog.Logger
}

// dayTotals tracks how many chunks the loop iterated, backfilled, and
// skipped. The caller logs one terminal summary line from these.
type dayTotals struct {
	total    int
	inserted int
	skipped  int
}

// loop walks [job.from, job.to] inclusive and invokes one chunk per
// day. Extracted from run() so run() stays under the gocyclo ceiling.
func (j *backfillJob) loop(ctx context.Context) (dayTotals, error) {
	var t dayTotals

	for day := j.from; !day.After(j.to); day = day.AddDate(0, 0, 1) {
		select {
		case <-ctx.Done():
			return t, fmt.Errorf("interrupted at %s: %w", day.Format(dateLayout), ctx.Err())
		default:
		}

		t.total++

		if err := j.processDay(ctx, day, &t); err != nil {
			return t, err
		}
	}

	return t, nil
}

// processDay handles one (site_id, day) chunk: idempotency check,
// optional INSERT, log line. Mutates t to record skipped vs inserted.
func (j *backfillJob) processDay(ctx context.Context, day time.Time, t *dayTotals) error {
	if !j.skipChk && !j.dryRun {
		needsBackfill, checkErr := chunkNeedsBackfill(ctx, j.conn, j.database, day)
		if checkErr != nil {
			return fmt.Errorf("idempotency check %s: %w", day.Format(dateLayout), checkErr)
		}

		if !needsBackfill {
			t.skipped++

			j.logger.Info("skip", "day", day.Format(dateLayout), "reason", "daily_geo views match events_raw")

			return nil
		}
	}

	start := time.Now()

	rowsAffected, err := backfillDay(ctx, j.conn, j.database, day, j.dryRun)
	if err != nil {
		return fmt.Errorf("backfill %s: %w", day.Format(dateLayout), err)
	}

	t.inserted++

	j.logger.Info("backfilled", "day", day.Format(dateLayout), "rollup_rows", rowsAffected, "duration", time.Since(start))

	return nil
}

// loadConfig reads only the clickhouse.* keys from the YAML config —
// the backfill binary doesn't need the full config surface and pulling
// in main.go's parser would be a coupling cost.
func loadConfig(path string) (storage.Config, error) {
	v := viper.New()
	v.SetConfigFile(path)

	if err := v.ReadInConfig(); err != nil {
		return storage.Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	addr := v.GetString("clickhouse.addr")
	if addr == "" {
		return storage.Config{}, errors.New("clickhouse.addr empty in config")
	}

	database := v.GetString("clickhouse.database")
	if database == "" {
		database = "statnive"
	}

	return storage.Config{
		Addrs:    []string{addr},
		Database: database,
		Username: v.GetString("clickhouse.username"),
		Password: v.GetString("clickhouse.password"),
	}, nil
}

// assertDailyGeoExists fails fast if migration 019 has not been applied
// on the target ClickHouse — the operator should deploy the binary
// first, watch healthz, and only then run this tool.
func assertDailyGeoExists(ctx context.Context, conn driver.Conn, database string) error {
	row := conn.QueryRow(ctx, `
		SELECT count()
		FROM system.tables
		WHERE database = ? AND name = 'daily_geo'
	`, database)

	var n uint64
	if err := row.Scan(&n); err != nil {
		return fmt.Errorf("probe daily_geo: %w", err)
	}

	if n == 0 {
		return errors.New("daily_geo table missing — apply migration 019 by starting the main binary first")
	}

	return nil
}

// chunkNeedsBackfill returns true when daily_geo's sum(views) for the
// given day is below the events_raw count for that day. We compare the
// totals across all site_ids — that's the same shape the runbook
// reconcile step (PLAN.md v1.1-geo § Staged deploy SOP step 6) uses.
//
// The check is conservative: even a 1-row mismatch triggers a full
// re-insert of the chunk. That's safe because re-inserting the same
// (site_id, day, country, province, city, time) merges idempotently
// into AggregatingMergeTree for the HLL column. The SUM columns
// double-count without --skip-idempotency-check, so this guard is
// the safety contract.
func chunkNeedsBackfill(ctx context.Context, conn driver.Conn, database string, day time.Time) (bool, error) {
	dayStr := day.Format(dateLayout)

	rollupSQL := fmt.Sprintf(`
		SELECT toUInt64(sum(views))
		FROM %s.daily_geo
		WHERE day = toDate(?)
	`, database)

	var rollupViews uint64
	if err := conn.QueryRow(ctx, rollupSQL, dayStr).Scan(&rollupViews); err != nil {
		return false, fmt.Errorf("rollup count: %w", err)
	}

	rawSQL := fmt.Sprintf(`
		SELECT count()
		FROM %s.events_raw
		WHERE toDate(time) = toDate(?) AND is_bot = 0
	`, database)

	var rawCount uint64
	if err := conn.QueryRow(ctx, rawSQL, dayStr).Scan(&rawCount); err != nil {
		return false, fmt.Errorf("raw count: %w", err)
	}

	return rollupViews < rawCount, nil
}

// backfillDay runs the INSERT INTO daily_geo SELECT FROM events_raw
// statement for the given day. Returns the number of rollup rows
// inserted (CH reports the GROUP BY output cardinality).
func backfillDay(ctx context.Context, conn driver.Conn, database string, day time.Time, dryRun bool) (uint64, error) {
	dayStr := day.Format(dateLayout)

	sql := fmt.Sprintf(`
		INSERT INTO %s.daily_geo
		SELECT
			site_id,
			toDate(time)                                        AS day,
			country_code,
			province,
			city,
			count()                                             AS views,
			uniqCombined64State(visitor_hash)                   AS visitors_state,
			sum(is_goal)                                        AS goals,
			sum(event_value)                                    AS revenue
		FROM %s.events_raw
		WHERE toDate(time) = toDate(?) AND is_bot = 0
		GROUP BY site_id, day, country_code, province, city
	`, database, database)

	if dryRun {
		fmt.Println(strings.TrimSpace(sql), "-- day:", dayStr)

		return 0, nil
	}

	if err := conn.Exec(ctx, sql, dayStr); err != nil {
		return 0, err
	}

	// Re-read the rollup row count for the day so the log line is
	// informative. The exec above doesn't return a row count.
	row := conn.QueryRow(ctx, fmt.Sprintf(`
		SELECT count()
		FROM %s.daily_geo
		WHERE day = toDate(?)
	`, database), dayStr)

	var n uint64
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("post-insert count: %w", err)
	}

	return n, nil
}
