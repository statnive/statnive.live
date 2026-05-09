// Package storagetest holds test-only helpers for seeding rollup data
// into a live ClickHouse without going through the ingest pipeline.
// Production code never imports this package.
package storagetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// SeedSite inserts a site row so handler.LookupSiteIDByHostname can
// resolve the hostname during test ingest.
//
// Cleans by BOTH site_id AND hostname so two tests using the same
// hostname under different site_ids don't end up with multiple active
// rows for the same hostname. handler.LookupSiteIDByHostname does
// `SELECT site_id FROM sites WHERE hostname = ? AND enabled = 1 LIMIT 1`,
// which returns a NONDETERMINISTIC row when multiple match — that
// silently routed multitenant's events to the wrong site_id and was
// the root cause of the PR #29 CI flake (see commit cca29f3 debug log).
//
//nolint:revive // test helper — *testing.T first is idiomatic for t.Helper()
func SeedSite(t *testing.T, ctx context.Context, conn driver.Conn, siteID uint32, hostname string) {
	t.Helper()

	if err := conn.Exec(ctx,
		`ALTER TABLE statnive.sites DELETE WHERE site_id = ? OR hostname = ? SETTINGS mutations_sync = 2`,
		siteID, hostname,
	); err != nil {
		t.Logf("clean sites (ok on first run): %v", err)
	}

	if err := conn.Exec(ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, enabled) VALUES (?, ?, ?, 1)`,
		siteID, hostname, fmt.Sprintf("site-%d", siteID),
	); err != nil {
		t.Fatalf("seed site %d: %v", siteID, err)
	}
}

// CleanSiteEvents removes every events_raw + rollup row for siteID.
// Idempotent. mutations_sync=2 makes the DELETE block until the
// merge applies so subsequent SELECTs see the clean state.
//
//nolint:revive // test helper — *testing.T first is idiomatic for t.Helper()
func CleanSiteEvents(t *testing.T, ctx context.Context, conn driver.Conn, siteID uint32) {
	t.Helper()

	tables := []string{
		"statnive.events_raw",
		"statnive.hourly_visitors",
		"statnive.daily_pages",
		"statnive.daily_sources",
	}

	for _, tbl := range tables {
		_ = conn.Exec(ctx,
			fmt.Sprintf("ALTER TABLE %s DELETE WHERE site_id = ? SETTINGS mutations_sync = 2", tbl),
			siteID,
		)
	}
}

// SeedEvent represents one synthetic event the test wants to land in
// every relevant rollup. Insert via WriteEvents. Revenue is stored as a
// currency-neutral integer; the SPA formats it using the site's
// currency code (display label only).
type SeedEvent struct {
	SiteID       uint32
	Time         time.Time
	Pathname     string
	Referrer     string
	ReferrerName string
	Channel      string
	UTMCampaign  string
	IsGoal       bool
	Revenue      uint64
	VisitorHash  [16]byte
}

// WriteEvents inserts events into events_raw and forces materialized
// view propagation by selecting from the views' source. After this
// call the rollups (hourly_visitors / daily_pages / daily_sources) all
// reflect the seeded events.
//
// We insert directly rather than going through the ingest pipeline so
// tests can construct deterministic rollup state without depending on
// the WAL + consumer + 6-stage pipeline. Time, channel, etc. are all
// caller-controlled.
//
//nolint:revive // test helper — *testing.T first is idiomatic for t.Helper()
func WriteEvents(t *testing.T, ctx context.Context, conn driver.Conn, events []SeedEvent) {
	t.Helper()

	if len(events) == 0 {
		return
	}

	batch, err := conn.PrepareBatch(ctx, `INSERT INTO statnive.events_raw (
		site_id, time, user_id_hash, cookie_id, visitor_hash, hostname, pathname,
		title, referrer, referrer_name, channel, utm_source, utm_medium,
		utm_campaign, utm_content, utm_term, province, city, country_code,
		isp, carrier, os, browser, device_type, viewport_width, event_type,
		event_name, event_value, is_goal, is_new, prop_keys, prop_vals,
		user_segment, is_bot
	)`)
	if err != nil {
		t.Fatalf("prepare batch: %v", err)
	}

	for _, e := range events {
		var goal uint8
		if e.IsGoal {
			goal = 1
		}

		if err := batch.Append(
			e.SiteID,
			e.Time,
			"",               // user_id_hash
			"",               // cookie_id
			e.VisitorHash[:], // visitor_hash
			"",               // hostname (not used by rollups)
			e.Pathname,
			"", // title
			e.Referrer,
			e.ReferrerName,
			e.Channel,
			"", // utm_source
			"", // utm_medium
			e.UTMCampaign,
			"",         // utm_content
			"",         // utm_term
			"",         // province
			"",         // city
			"--",       // country_code
			"",         // isp
			"",         // carrier
			"",         // os
			"",         // browser
			"",         // device_type
			uint16(0),  // viewport_width
			"pageview", // event_type
			"pageview", // event_name
			e.Revenue,
			goal,
			uint8(1),   // is_new
			[]string{}, // prop_keys
			[]string{}, // prop_vals
			"",         // user_segment
			uint8(0),   // is_bot
		); err != nil {
			t.Fatalf("append seed event: %v", err)
		}
	}

	if err := batch.Send(); err != nil {
		t.Fatalf("send batch: %v", err)
	}

	// Materialized views are pushed at INSERT time, but the rollup
	// rows aren't immediately query-able until the parts are merged.
	// OPTIMIZE FINAL forces the rollup parts to be merged synchronously
	// so subsequent SELECTs see the aggregated state. This is
	// expensive in production (Architecture Rule: insert-optimize-avoid-final)
	// but acceptable in tests.
	for _, tbl := range []string{
		"statnive.hourly_visitors",
		"statnive.daily_pages",
		"statnive.daily_sources",
	} {
		if err := conn.Exec(ctx, "OPTIMIZE TABLE "+tbl+" FINAL"); err != nil {
			t.Logf("optimize %s: %v", tbl, err)
		}
	}
}
