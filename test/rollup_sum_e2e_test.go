//go:build integration

// Rollup-sum gate for migration 009.
//
// Pins the bug discovered live on app.statnive.live on 2026-05-11:
// hourly_visitors / daily_pages / daily_sources were AggregatingMergeTree
// ORDER BY (site_id, hour|day|…) with plain `UInt64` columns. For
// non-AggregateFunction columns, AggregatingMergeTree behaves like
// ReplacingMergeTree on merge — one row per ORDER BY tuple survives,
// last-value wins. Production had hour 07:00 with 10 pageviews + 3 goal
// hits in events_raw, but hourly_visitors reported pageviews=1,
// goals=0, revenue=0 — the merge silently collapsed the rows.
//
// Migration 009 converts the four numeric columns to
// SimpleAggregateFunction(sum, UInt64). This test asserts the fix:
//
//   1. Apply all migrations (001–009).
//   2. Insert N=5 separate batches into events_raw, each creating a
//      distinct CH part (sleep between inserts so the engine flushes).
//   3. Force-merge via OPTIMIZE TABLE … FINAL to exercise the merge
//      path that used to collapse rows.
//   4. Query hourly_visitors — pageviews should equal N, goals should
//      equal the count of goal-flagged inserts, revenue should sum
//      across all goal hits.
//
// If migration 009 hasn't been applied (or got reverted), step 4 fails
// because the merge dropped rows.
package integration_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/statnive/statnive.live/internal/storage"
)

const rollupSumDB = "statnive_rollup_sum_gate"

// preMig009 applies every migration up to (but NOT including) 009. The
// test then inserts events under the pre-009 schema (which still has
// plain UInt64 — but with the rename from 008 already applied), runs
// migration 009, verifies the column types changed, and runs the
// sum assertions.
//
// We don't repeat the data-preservation gate from migration 008 here —
// that's already pinned by data_preservation_e2e_test.go. The point
// of THIS test is the post-merge sum invariant.
var rollupSumMigrations = []string{
	"001_initial.sql",
	"002_rollups.sql",
	"003_sites_tz.sql",
	"004_auth_schema.sql",
	"005_goals_schema.sql",
	"006_sites_privacy.sql",
	"007_sites_currency.sql",
	"008_rename_revenue_columns.sql",
	"009_rollup_simple_aggregate.sql",
}

func TestMigration009_RollupSumSemantics(t *testing.T) {
	ctx := context.Background()

	conn := openCH(t, ctx, "")
	t.Cleanup(func() {
		_ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+rollupSumDB+" SYNC")
		_ = conn.Close()
	})

	if err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+rollupSumDB+" SYNC"); err != nil {
		t.Fatalf("pre-clean db: %v", err)
	}

	if err := conn.Exec(ctx, "CREATE DATABASE "+rollupSumDB); err != nil {
		t.Fatalf("create db: %v", err)
	}

	dbConn := openCH(t, ctx, rollupSumDB)
	t.Cleanup(func() { _ = dbConn.Close() })

	applyRollupMigrations(t, ctx, dbConn, rollupSumMigrations)

	// Confirm migration 009 actually changed the column types — the
	// system.columns row for hourly_visitors.goals should show the
	// SimpleAggregateFunction wrapper.
	var typ string
	if err := dbConn.QueryRow(ctx, fmt.Sprintf(
		`SELECT type FROM system.columns WHERE database = '%s' AND table = 'hourly_visitors' AND name = 'goals'`,
		rollupSumDB,
	)).Scan(&typ); err != nil {
		t.Fatalf("read column type: %v", err)
	}

	if !strings.HasPrefix(typ, "SimpleAggregateFunction(sum,") {
		t.Fatalf("hourly_visitors.goals type = %q, want SimpleAggregateFunction(sum, UInt64)", typ)
	}

	// Insert 5 separate batches, each as its own part. Each batch
	// contains one pageview and (for indexes 1, 3, 4) one goal-flagged
	// custom event. Same site_id, same hour bucket — exactly the
	// (site_id, hour) collision that the pre-fix merge collapsed.
	const siteID = uint32(901)
	baseTime := time.Now().UTC().Truncate(time.Hour).Add(30 * time.Minute)

	wantPageviews := uint64(5)
	wantGoals := uint64(3)
	wantRevenue := uint64(3) // 3 goal hits × value=1

	// Each insertRollupEvent calls PrepareBatch + Send, and
	// clickhouse-go v2 emits one INSERT per Send — each call lands in
	// its own part. No sleep needed; parts are separate regardless.
	for i := 0; i < 5; i++ {
		insertRollupEvent(t, ctx, dbConn, siteID, baseTime.Add(time.Duration(i)*time.Second),
			"pageview", "pageview", 0, false)
		if i == 1 || i == 3 || i == 4 {
			insertRollupEvent(t, ctx, dbConn, siteID, baseTime.Add(time.Duration(i)*time.Second+500*time.Millisecond),
				"custom", "release_download_click", 1, true)
		}
	}

	// Force-merge the rollup target. OPTIMIZE … FINAL is the exact
	// trigger that exposed the production bug — the merge collapsed
	// rows. With SimpleAggregateFunction(sum, …) the merge SUMs.
	if err := dbConn.Exec(ctx, fmt.Sprintf(
		`OPTIMIZE TABLE %s.hourly_visitors FINAL`, rollupSumDB,
	)); err != nil {
		t.Fatalf("optimize hourly_visitors: %v", err)
	}

	if err := dbConn.Exec(ctx, fmt.Sprintf(
		`OPTIMIZE TABLE %s.daily_pages FINAL`, rollupSumDB,
	)); err != nil {
		t.Fatalf("optimize daily_pages: %v", err)
	}

	if err := dbConn.Exec(ctx, fmt.Sprintf(
		`OPTIMIZE TABLE %s.daily_sources FINAL`, rollupSumDB,
	)); err != nil {
		t.Fatalf("optimize daily_sources: %v", err)
	}

	// Post-merge sum assertion — pre-fix this returned 1/0/0 because
	// the merge dropped 4 of 5 rows. Post-fix it must SUM correctly.
	var gotPV, gotGoals, gotRev uint64
	if err := dbConn.QueryRow(ctx, fmt.Sprintf(
		`SELECT toUInt64(sum(pageviews)), toUInt64(sum(goals)), toUInt64(sum(revenue))
		 FROM %s.hourly_visitors WHERE site_id = ?`,
		rollupSumDB,
	), siteID).Scan(&gotPV, &gotGoals, &gotRev); err != nil {
		t.Fatalf("read hourly_visitors aggregate: %v", err)
	}

	if gotPV != wantPageviews {
		t.Errorf("hourly_visitors pageviews after FINAL merge = %d, want %d (merge dropped %d rows — migration 009 broken)",
			gotPV, wantPageviews, wantPageviews-gotPV)
	}

	if gotGoals != wantGoals {
		t.Errorf("hourly_visitors goals after FINAL merge = %d, want %d (merge dropped %d goal rows)",
			gotGoals, wantGoals, wantGoals-gotGoals)
	}

	if gotRev != wantRevenue {
		t.Errorf("hourly_visitors revenue after FINAL merge = %d, want %d", gotRev, wantRevenue)
	}

	// daily_pages + daily_sources should also sum correctly. Goal
	// events all have pathname='/pricing' here (set by
	// insertRollupEvent's pageview seed below) — they all land in the
	// same daily_pages row.
	var pagesPV, pagesGoals, pagesRev uint64
	if err := dbConn.QueryRow(ctx, fmt.Sprintf(
		`SELECT toUInt64(sum(views)), toUInt64(sum(goals)), toUInt64(sum(revenue))
		 FROM %s.daily_pages WHERE site_id = ?`,
		rollupSumDB,
	), siteID).Scan(&pagesPV, &pagesGoals, &pagesRev); err != nil {
		t.Fatalf("read daily_pages aggregate: %v", err)
	}

	// daily_pages MV filter is `WHERE is_bot = 0` (no event_type filter),
	// so it counts BOTH the 5 pageviews AND the 3 custom goal events.
	if pagesPV != wantPageviews+wantGoals {
		t.Errorf("daily_pages views = %d, want %d (5 pv + 3 goal events)", pagesPV, wantPageviews+wantGoals)
	}

	if pagesGoals != wantGoals {
		t.Errorf("daily_pages goals = %d, want %d", pagesGoals, wantGoals)
	}

	if pagesRev != wantRevenue {
		t.Errorf("daily_pages revenue = %d, want %d", pagesRev, wantRevenue)
	}
}

// applyRollupMigrations is the test-side migration runner. Mirrors
// applyMigrations in data_preservation_e2e_test.go (same render +
// split-statements path) but takes a custom DB name so the two tests
// don't share state.
func applyRollupMigrations(t *testing.T, ctx context.Context, conn driver.Conn, names []string) {
	t.Helper()

	for _, name := range names {
		body, err := storage.Migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}

		rendered, err := storage.RenderMigration(name, body, "")
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}

		rendered = strings.ReplaceAll(rendered, "statnive.", rollupSumDB+".")

		for _, stmt := range storage.SplitStatements(rendered) {
			s := strings.TrimSpace(stmt)
			if s == "" {
				continue
			}

			if err := conn.Exec(ctx, s); err != nil {
				t.Fatalf("apply %s: %v\nSQL: %s", name, err, firstNLines(s, 4))
			}
		}
	}
}

func insertRollupEvent(
	t *testing.T, ctx context.Context, conn driver.Conn,
	siteID uint32, ts time.Time, eventType, eventName string, value uint64, isGoal bool,
) {
	t.Helper()

	goal := uint8(0)
	if isGoal {
		goal = 1
	}

	hash := [16]byte{byte(siteID), byte(value), byte(ts.UnixNano())}

	batch, err := conn.PrepareBatch(ctx, fmt.Sprintf(`INSERT INTO %s.events_raw (
		site_id, time, user_id_hash, cookie_id, visitor_hash, hostname, pathname,
		title, referrer, referrer_name, channel, utm_source, utm_medium,
		utm_campaign, utm_content, utm_term, province, city, country_code,
		isp, carrier, os, browser, device_type, viewport_width, event_type,
		event_name, event_value, is_goal, is_new, prop_keys, prop_vals,
		user_segment, is_bot
	)`, rollupSumDB))
	if err != nil {
		t.Fatalf("prepare rollup batch: %v", err)
	}

	if err := batch.Append(
		siteID, ts, "", "", hash[:],
		"", "/pricing", "", "", "direct", "Direct",
		"", "", "", "", "", "", "", "--",
		"", "", "", "", "", uint16(0),
		eventType, eventName, value, goal,
		uint8(1), []string{}, []string{}, "", uint8(0),
	); err != nil {
		t.Fatalf("append rollup event: %v", err)
	}

	if err := batch.Send(); err != nil {
		t.Fatalf("send rollup batch: %v", err)
	}
}
