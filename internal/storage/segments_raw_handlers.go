package storage

import (
	"context"
	"fmt"
)

// This file holds the *FromRaw variants for the 6 dashboard handlers
// other than Overview (which lives in queries.go alongside the original
// rollup path because the file pre-dated this carve-out). All FromRaw
// implementations follow the same shape:
//
//   1. Build WHERE via whereWithProps so Architecture Rule 8 stays
//      satisfied (whereTimeAndTenant is the first clause) and Map
//      predicates ride at the end.
//   2. Scan events_raw with the appropriate GROUP BY mirroring the
//      rollup that the rollup-path query reads from.
//   3. Cap memory + execution time via SETTINGS so a long range can't
//      sink the cluster.
//
// Architecture Rule 1 carve-out: events_raw reads here are sanctioned
// by the plan's § 4 cost-guardrail block. The tenancy-grep gate
// allowlists this file (and overviewFromRaw in queries.go) by name.
// New events_raw queries elsewhere must justify themselves.

const (
	// rawMaxExecutionSec bounds any single raw-fallback query so a
	// pathological 90-day scan can't tie up the CH worker for long.
	// 30 s exceeds the 5 s p99 budget for the warm-cache path; cold
	// scans on the design ceiling (200 M events/day × 90 days) finish
	// inside this when the bloom indexes from a future migration kick in.
	rawMaxExecutionSec = 30

	// rawMaxMemoryBytes caps the per-query memory at 8 GB (8 * 1 << 30).
	// 8c/32GB single-node bound; on the 32c/128GB P5 cluster this can
	// be relaxed per the operator's SETTINGS profile.
	rawMaxMemoryBytes = 8 * 1024 * 1024 * 1024
)

// sourcesFromRaw scans events_raw with GROUP BY referrer_name, channel
// — same shape as Sources but reading the raw table because the rollup
// has no Map columns and can't answer prop-filtered queries.
func (s *clickhouseStore) sourcesFromRaw(ctx context.Context, f *Filter) ([]SourceRow, error) {
	where, args := whereWithProps(f, "time", eventsRawCols)

	q := fmt.Sprintf(`
		SELECT
			referrer_name,
			channel,
			toUInt64(count())                          AS views,
			toUInt64(uniqCombined64(visitor_hash))     AS visitors,
			toUInt64(sumIf(1, is_goal = 1))            AS goals,
			toUInt64(sumIf(event_value, is_goal = 1))  AS revenue
		FROM statnive.events_raw %s -- raw-fallback OK (segments prop filter)
		GROUP BY referrer_name, channel
		%s
		LIMIT ?
		SETTINGS max_execution_time = %d, max_memory_usage = %d
	`,
		where,
		orderClause(f, sourcesSortable, "revenue DESC, views DESC"),
		rawMaxExecutionSec, rawMaxMemoryBytes,
	)

	args = append(args, f.EffectiveLimit())

	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sources-raw query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []SourceRow{}

	for rows.Next() {
		var r SourceRow
		if scanErr := rows.Scan(&r.ReferrerName, &r.Channel, &r.Views, &r.Visitors, &r.Goals, &r.Revenue); scanErr != nil {
			return nil, fmt.Errorf("sources-raw scan: %w", scanErr)
		}

		r.RPV = rpv(r.Revenue, r.Visitors)
		out = append(out, r)
	}

	return out, rows.Err()
}

// sourcesByChannelFromRaw groups events_raw by channel only.
func (s *clickhouseStore) sourcesByChannelFromRaw(ctx context.Context, f *Filter) ([]SourceChannelRow, error) {
	where, args := whereWithProps(f, "time", eventsRawCols)

	q := fmt.Sprintf(`
		SELECT
			channel,
			toUInt64(count())                          AS views,
			toUInt64(uniqCombined64(visitor_hash))     AS visitors,
			toUInt64(sumIf(1, is_goal = 1))            AS goals,
			toUInt64(sumIf(event_value, is_goal = 1))  AS revenue
		FROM statnive.events_raw %s -- raw-fallback OK (segments prop filter)
		GROUP BY channel
		%s
		SETTINGS max_execution_time = %d, max_memory_usage = %d
	`,
		where,
		orderClause(f, sourcesByChannelSortable, "revenue DESC, views DESC"),
		rawMaxExecutionSec, rawMaxMemoryBytes,
	)

	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sources-by-channel-raw query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []SourceChannelRow{}

	for rows.Next() {
		var r SourceChannelRow
		if scanErr := rows.Scan(&r.Channel, &r.Views, &r.Visitors, &r.Goals, &r.Revenue); scanErr != nil {
			return nil, fmt.Errorf("sources-by-channel-raw scan: %w", scanErr)
		}

		r.RPV = rpv(r.Revenue, r.Visitors)
		out = append(out, r)
	}

	return out, rows.Err()
}

// pagesFromRaw groups events_raw by pathname.
func (s *clickhouseStore) pagesFromRaw(ctx context.Context, f *Filter) ([]PageRow, error) {
	where, args := whereWithProps(f, "time", eventsRawCols)

	q := fmt.Sprintf(`
		SELECT
			pathname,
			toUInt64(count())                          AS views,
			toUInt64(uniqCombined64(visitor_hash))     AS visitors,
			toUInt64(sumIf(1, is_goal = 1))            AS goals,
			toUInt64(sumIf(event_value, is_goal = 1))  AS revenue
		FROM statnive.events_raw %s -- raw-fallback OK (segments prop filter)
		GROUP BY pathname
		%s
		LIMIT ?
		SETTINGS max_execution_time = %d, max_memory_usage = %d
	`,
		where,
		orderClause(f, pagesSortable, "revenue DESC, views DESC"),
		rawMaxExecutionSec, rawMaxMemoryBytes,
	)

	args = append(args, f.EffectiveLimit())

	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("pages-raw query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []PageRow{}

	for rows.Next() {
		var r PageRow
		if scanErr := rows.Scan(&r.Pathname, &r.Views, &r.Visitors, &r.Goals, &r.Revenue); scanErr != nil {
			return nil, fmt.Errorf("pages-raw scan: %w", scanErr)
		}

		r.RPV = rpv(r.Revenue, r.Visitors)
		out = append(out, r)
	}

	return out, rows.Err()
}

// seoFromRaw is the organic-search daily trend over events_raw. Pre-
// filters to channel = ChannelOrganicSearch to match the rollup path.
// WITH FILL emits one row per day in the [from, to) range so the SPA
// renders a continuous series even when the filter narrows traffic.
func (s *clickhouseStore) seoFromRaw(ctx context.Context, f *Filter) ([]SEORow, error) {
	where, args := whereWithProps(f, "time", eventsRawCols)

	// Append channel constraint after the prop predicates so the
	// where-builder allowlist stays minimal — channel sits in
	// eventsRawCols already but only fires when f.Channel is set; for
	// SEO we hard-pin it.
	where += " AND channel = ?"

	args = append(args, ChannelOrganicSearch)

	q := fmt.Sprintf(`
		SELECT
			toStartOfDay(time)                         AS day,
			toUInt64(count())                          AS views,
			toUInt64(uniqCombined64(visitor_hash))     AS visitors,
			toUInt64(sumIf(1, is_goal = 1))            AS goals,
			toUInt64(sumIf(event_value, is_goal = 1))  AS revenue
		FROM statnive.events_raw %s -- raw-fallback OK (segments prop filter)
		GROUP BY day
		ORDER BY day ASC
		   WITH FILL FROM toStartOfDay(?) TO toStartOfDay(?) STEP toIntervalDay(1)
		SETTINGS max_execution_time = %d, max_memory_usage = %d
	`,
		where,
		rawMaxExecutionSec, rawMaxMemoryBytes,
	)

	args = append(args, f.From, f.To)

	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("seo-raw query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []SEORow{}

	for rows.Next() {
		var r SEORow
		if scanErr := rows.Scan(&r.Day, &r.Views, &r.Visitors, &r.Goals, &r.Revenue); scanErr != nil {
			return nil, fmt.Errorf("seo-raw scan: %w", scanErr)
		}

		out = append(out, r)
	}

	return out, rows.Err()
}

// campaignsFromRaw groups by the full UTM tuple + channel.
func (s *clickhouseStore) campaignsFromRaw(ctx context.Context, f *Filter) ([]CampaignRow, error) {
	where, args := whereWithProps(f, "time", eventsRawCols)

	q := fmt.Sprintf(`
		SELECT
			utm_campaign,
			utm_source,
			utm_medium,
			utm_content,
			utm_term,
			channel,
			toUInt64(count())                          AS views,
			toUInt64(uniqCombined64(visitor_hash))     AS visitors,
			toUInt64(sumIf(1, is_goal = 1))            AS goals,
			toUInt64(sumIf(event_value, is_goal = 1))  AS revenue
		FROM statnive.events_raw %s -- raw-fallback OK (segments prop filter)
		GROUP BY utm_campaign, utm_source, utm_medium, utm_content, utm_term, channel
		%s
		LIMIT ?
		SETTINGS max_execution_time = %d, max_memory_usage = %d
	`,
		where,
		orderClause(f, campaignsSortable, "revenue DESC, views DESC"),
		rawMaxExecutionSec, rawMaxMemoryBytes,
	)

	args = append(args, f.EffectiveLimit())

	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("campaigns-raw query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []CampaignRow{}

	for rows.Next() {
		var r CampaignRow
		if scanErr := rows.Scan(&r.UTMCampaign, &r.UTMSource, &r.UTMMedium, &r.UTMContent, &r.UTMTerm, &r.Channel, &r.Views, &r.Visitors, &r.Goals, &r.Revenue); scanErr != nil {
			return nil, fmt.Errorf("campaigns-raw scan: %w", scanErr)
		}

		r.RPV = rpv(r.Revenue, r.Visitors)
		out = append(out, r)
	}

	return out, rows.Err()
}

// trendFromRaw is the all-traffic daily series with WITH FILL.
func (s *clickhouseStore) trendFromRaw(ctx context.Context, f *Filter) ([]DailyPoint, error) {
	where, args := whereWithProps(f, "time", eventsRawCols)

	q := fmt.Sprintf(`
		SELECT
			toStartOfDay(time)                                AS day,
			toUInt64(uniqCombined64(visitor_hash))            AS visitors,
			toUInt64(count())                                 AS pageviews,
			toUInt64(sumIf(1, is_goal = 1))                   AS goals,
			toUInt64(sumIf(event_value, is_goal = 1))         AS revenue
		FROM statnive.events_raw %s -- raw-fallback OK (segments prop filter)
		GROUP BY day
		ORDER BY day ASC
		   WITH FILL FROM toStartOfDay(?) TO toStartOfDay(?) STEP toIntervalDay(1)
		SETTINGS max_execution_time = %d, max_memory_usage = %d
	`,
		where,
		rawMaxExecutionSec, rawMaxMemoryBytes,
	)

	args = append(args, f.From, f.To)

	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("trend-raw query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []DailyPoint{}

	for rows.Next() {
		var p DailyPoint
		if scanErr := rows.Scan(&p.Day, &p.Visitors, &p.Pageviews, &p.Goals, &p.Revenue); scanErr != nil {
			return nil, fmt.Errorf("trend-raw scan: %w", scanErr)
		}

		out = append(out, p)
	}

	return out, rows.Err()
}

// realtimeFromRaw is the active-visitors widget over events_raw for the
// last 5 minutes when a prop filter is active. Realtime's existing
// rollup path uses the current hour bucket; for the raw fallback we
// tighten the window to 5 minutes because (a) the bloom indexes don't
// help at 1-hour scale and (b) operators expect realtime to refresh
// fast under interactive prop-filter changes.
func (s *clickhouseStore) realtimeFromRaw(ctx context.Context, f *Filter) (*RealtimeResult, error) {
	// Realtime ignores f.From / f.To and looks at the trailing 5 min.
	where := "WHERE site_id = ? AND time >= now() - INTERVAL 5 MINUTE"
	args := []any{f.SiteID}

	where, args = appendPropPredicates(where, args, "hit_props", f.HitProps)
	where, args = appendPropPredicates(where, args, "session_props", f.SessionProps)
	where, args = appendPropPredicates(where, args, "user_props", f.UserProps)

	if f.Channel != "" {
		where += " AND channel = ?"

		args = append(args, f.Channel)
	}

	q := fmt.Sprintf(`
		SELECT
			toStartOfHour(now())                       AS hour_utc,
			toUInt64(uniqCombined64(visitor_hash))     AS active_visitors,
			toUInt64(count())                          AS pageviews_last_hr
		FROM statnive.events_raw %s -- raw-fallback OK (segments prop filter)
		SETTINGS max_execution_time = %d, max_memory_usage = %d
	`, where, rawMaxExecutionSec, rawMaxMemoryBytes)

	row := s.conn.QueryRow(ctx, q, args...)

	var out RealtimeResult
	if err := row.Scan(&out.HourUTC, &out.ActiveVisitors, &out.PageviewsLastHr); err != nil {
		return nil, fmt.Errorf("realtime-raw query: %w", err)
	}

	return &out, nil
}
