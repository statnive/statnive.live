//go:build integration

// Data-preservation gate for migration 008 (revenue_rials → revenue).
//
// The user's explicit ship-blocking requirement: "doesn't break current
// data of users." This test is the mechanical proof.
//
// Procedure:
//
//  1. Bootstrap a fresh ClickHouse database isolated from the other
//     integration tests (the canonical 'statnive' DB will already have
//     migration 008 applied by other test files via package-init
//     migration runs, so we need an unrelated DB to simulate the
//     pre-migration state).
//  2. Apply migrations 001–007 (everything before the rename).
//  3. Seed a known-fixed set of events into events_raw, force MV
//     propagation into hourly_visitors / daily_pages / daily_sources.
//  4. Capture per-site sum(revenue_rials) from each rollup target —
//     this is the **pre-migration oracle**.
//  5. Apply migration 008 (RENAME COLUMN + MODIFY QUERY).
//  6. Re-query per-site sum(revenue) from each rollup target.
//     **Byte-equal to the pre-migration oracle is the gate.**
//  7. Insert one more event. Verify it lands in the renamed column
//     (proves MODIFY QUERY left the MV plumbing intact).
//
// Failure here is release-blocking: it means migration 008 silently
// corrupts historical aggregate state during the rename.
package integration_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/statnive/statnive.live/internal/storage"
)

// Use a distinct database name so we don't trample on the canonical
// `statnive` DB that other integration tests also touch.
const dataPreservationDB = "statnive_dp_gate"

// preMig008 is the prefix list of migrations the test applies before
// 008 to simulate the pre-rename world. Mirrors what production looked
// like just before this PR's deploy.
var preMig008 = []string{
	"001_initial.sql",
	"002_rollups.sql",
	"003_sites_tz.sql",
	"004_auth_schema.sql",
	"005_goals_schema.sql",
	"006_sites_privacy.sql",
	"007_sites_currency.sql",
}

func TestMigration008_DataPreservation(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	conn := openCH(t, ctx, "")

	t.Cleanup(func() {
		_ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+dataPreservationDB+" SYNC")
		_ = conn.Close()
	})

	if err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+dataPreservationDB+" SYNC"); err != nil {
		t.Fatalf("pre-clean db: %v", err)
	}

	if err := conn.Exec(ctx, "CREATE DATABASE "+dataPreservationDB); err != nil {
		t.Fatalf("create db: %v", err)
	}

	dbConn := openCH(t, ctx, dataPreservationDB)
	t.Cleanup(func() { _ = dbConn.Close() })

	// Step 2 — apply migrations 001–007 only. We can't use the runner
	// because it walks all embedded migrations; instead we read each
	// SQL by name and exec the rendered statements.
	applyMigrations(t, ctx, dbConn, preMig008)

	// Step 3 — seed events into events_raw with the legacy revenue_rials
	// column populated via event_value (events_raw doesn't have its own
	// revenue_rials — the rollup is the only place the alias lived).
	site1, site2 := uint32(701), uint32(702)
	seedDPEvent(t, ctx, dbConn, site1, "/checkout", 1500, true)
	seedDPEvent(t, ctx, dbConn, site1, "/checkout", 2500, true)
	seedDPEvent(t, ctx, dbConn, site2, "/buy", 999, true)

	// Step 4 — pre-migration oracle: read sum(revenue_rials) from each
	// rollup target. Force MV propagation by reading from the source
	// (CH AggregatingMergeTree MVs propagate on insert; the GROUP BY
	// query merges any unmaterialized parts).
	type oracleRow struct {
		site  uint32
		hv    uint64
		dp    uint64
		ds    uint64
	}

	pre := func(suffix string) []oracleRow {
		var rows []oracleRow
		for _, site := range []uint32{site1, site2} {
			r := oracleRow{site: site}
			r.hv = scanRevenueSum(t, ctx, dbConn,
				fmt.Sprintf("SELECT toUInt64(sum(%s)) FROM %s.hourly_visitors WHERE site_id = %d", suffix, dataPreservationDB, site))
			r.dp = scanRevenueSum(t, ctx, dbConn,
				fmt.Sprintf("SELECT toUInt64(sum(%s)) FROM %s.daily_pages WHERE site_id = %d", suffix, dataPreservationDB, site))
			r.ds = scanRevenueSum(t, ctx, dbConn,
				fmt.Sprintf("SELECT toUInt64(sum(%s)) FROM %s.daily_sources WHERE site_id = %d", suffix, dataPreservationDB, site))
			rows = append(rows, r)
		}
		return rows
	}

	preOracle := pre("revenue_rials")

	// Sanity: the seeded events must have produced non-zero rollup
	// state, otherwise the gate's equality assertion is trivially
	// true. Site 1 has 1500+2500 = 4000 in revenue.
	if preOracle[0].dp == 0 {
		t.Fatalf("pre-migration oracle is zero — MVs didn't propagate")
	}

	// Step 5 — apply migration 008.
	applyMigrations(t, ctx, dbConn, []string{"008_rename_revenue_columns.sql"})

	// Step 6 — re-query under the new column name; assert byte-equal.
	postOracle := pre("revenue")

	for i, p := range preOracle {
		q := postOracle[i]
		if p.hv != q.hv {
			t.Errorf("hourly_visitors site=%d revenue: pre=%d post=%d (rename corrupted state)", p.site, p.hv, q.hv)
		}
		if p.dp != q.dp {
			t.Errorf("daily_pages site=%d revenue: pre=%d post=%d (rename corrupted state)", p.site, p.dp, q.dp)
		}
		if p.ds != q.ds {
			t.Errorf("daily_sources site=%d revenue: pre=%d post=%d (rename corrupted state)", p.site, p.ds, q.ds)
		}
	}

	// Step 7 — fresh insert post-migration must land in the renamed
	// column via the MODIFY-QUERY'd MV.
	seedDPEvent(t, ctx, dbConn, site1, "/post-mig", 7777, true)

	freshSum := scanRevenueSum(t, ctx, dbConn,
		fmt.Sprintf("SELECT toUInt64(sum(revenue)) FROM %s.daily_pages WHERE site_id = %d AND pathname = '/post-mig'",
			dataPreservationDB, site1))

	if freshSum != 7777 {
		t.Errorf("post-migration insert into daily_pages: got revenue=%d, want 7777 (MODIFY QUERY broke the MV)", freshSum)
	}

	// Step 8 — assert the goals.value column rename also preserved data
	// (smoke test; goals are unlikely to have rows in this fresh DB but
	// the schema must reflect the rename either way).
	var goalsCol string
	if err := dbConn.QueryRow(ctx,
		`SELECT name FROM system.columns WHERE database = ? AND table = 'goals' AND name = 'value'`,
		dataPreservationDB,
	).Scan(&goalsCol); err != nil || goalsCol != "value" {
		t.Errorf("goals.value column missing post-migration: name=%q err=%v", goalsCol, err)
	}

	logger.Info("migration 008 data-preservation gate passed",
		slog.Any("pre_oracle", preOracle),
		slog.Any("post_oracle", postOracle))
}

// openCH returns a clickhouse driver.Conn pinned to the given database.
// Empty database = default ('default') so the helper can drop/create
// our test DB.
func openCH(t *testing.T, ctx context.Context, database string) driver.Conn {
	t.Helper()

	opts := &clickhouse.Options{
		Addr: []string{clickhouseAddr()},
		Auth: clickhouse.Auth{
			Username: "default",
			Database: database,
		},
		DialTimeout: 5 * time.Second,
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		t.Fatalf("open clickhouse: %v", err)
	}

	if err := conn.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	return conn
}

// applyMigrations reads each named .sql from the embedded migration FS,
// renders the template (single-node mode), and execs each statement.
// Mirrors storage.MigrationRunner.applyOne but doesn't record the
// schema_migrations row — the test wants explicit control over which
// migrations are applied to simulate the pre-008 world.
func applyMigrations(t *testing.T, ctx context.Context, conn driver.Conn, names []string) {
	t.Helper()

	for _, name := range names {
		body, err := storage.Migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}

		// Render the embedded SQL with .Cluster="" (single-node mode),
		// then substitute the database name. Migrations hardcode
		// "statnive."; for this isolated test we point at our DB.
		rendered := renderForSingleNode(t, name, body)
		rendered = strings.ReplaceAll(rendered, "statnive.", dataPreservationDB+".")

		// Split on `;` and exec each non-empty statement.
		for _, stmt := range strings.Split(rendered, ";") {
			s := strings.TrimSpace(stmt)
			if s == "" || strings.HasPrefix(s, "--") {
				continue
			}

			// Strip full-line "--" comments inside the statement.
			lines := []string{}
			for _, line := range strings.Split(s, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "--") {
					continue
				}
				lines = append(lines, line)
			}
			s = strings.TrimSpace(strings.Join(lines, "\n"))
			if s == "" {
				continue
			}

			if err := conn.Exec(ctx, s); err != nil {
				t.Fatalf("apply %s: %v\nSQL: %s", name, err, firstNLines(s, 4))
			}
		}
	}
}

// renderForSingleNode runs the embedded SQL through text/template with
// .Cluster="" so the {{if .Cluster}} branches resolve to the
// single-node literal.
func renderForSingleNode(t *testing.T, name string, body []byte) string {
	t.Helper()

	type data struct{ Cluster string }

	tmpl, err := template.New(name).Option("missingkey=error").Parse(string(body))
	if err != nil {
		t.Fatalf("template parse %s: %v", name, err)
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data{Cluster: ""}); err != nil {
		t.Fatalf("template execute %s: %v", name, err)
	}

	return sb.String()
}

func firstNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func seedDPEvent(t *testing.T, ctx context.Context, conn driver.Conn, siteID uint32, pathname string, value uint64, isGoal bool) {
	t.Helper()

	goal := uint8(0)
	if isGoal {
		goal = 1
	}

	hash := [16]byte{byte(siteID), byte(value), byte(value >> 8)}

	if err := conn.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.events_raw (
		site_id, time, user_id_hash, cookie_id, visitor_hash, hostname, pathname,
		title, referrer, referrer_name, channel, utm_source, utm_medium,
		utm_campaign, utm_content, utm_term, province, city, country_code,
		isp, carrier, os, browser, device_type, viewport_width, event_type,
		event_name, event_value, is_goal, is_new, prop_keys, prop_vals,
		user_segment, is_bot
	) VALUES (?, ?, '', '', ?, '', ?, '', '', 'direct', 'Direct', '', '', '', '', '', '', '', '--', '', '', '', '', '', 0, 'pageview', 'pageview', ?, ?, 1, [], [], '', 0)`, dataPreservationDB),
		siteID, time.Now().UTC().Add(-1*time.Minute), hash[:], pathname, value, goal,
	); err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func scanRevenueSum(t *testing.T, ctx context.Context, conn driver.Conn, query string) uint64 {
	t.Helper()

	var n uint64
	if err := conn.QueryRow(ctx, query).Scan(&n); err != nil {
		t.Fatalf("scan %q: %v", query, err)
	}
	return n
}
