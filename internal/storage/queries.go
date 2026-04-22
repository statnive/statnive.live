package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ChannelOrganicSearch is the canonical channel name written to the
// rollups by the channel mapper (Step 12 of the 17-step decision tree
// in internal/enrich/channel.go). Defined here so the SEO query can
// reference it without a cross-package import that would create a
// storage→enrich dependency.
const ChannelOrganicSearch = "Organic Search"

// rpv computes revenue-per-visitor with safe division.
func rpv(revenue, visitors uint64) float64 {
	if visitors == 0 {
		return 0
	}

	return float64(revenue) / float64(visitors)
}

// NewClickhouseQueryStore wraps an existing ClickHouseStore connection
// (the same pool main.go opens for ingest) and exposes the read-only
// Store interface. We deliberately reuse the ingest pool rather than
// opening a second one — at SamplePlatform's 10–20M DAU the dashboard adds
// far less query load than the ingest path, and a separate pool would
// double the connection count for no isolation benefit.
func NewClickhouseQueryStore(s *ClickHouseStore) Store {
	return &clickhouseStore{conn: s.Conn()}
}

type clickhouseStore struct {
	conn driver.Conn
}

// whereTimeAndTenant emits the WHERE clause every read query MUST start
// with: `site_id = ? AND <timeColumn> >= ? AND <timeColumn> < ?`.
//
// timeColumn is the actual column name in the rollup (hourly_visitors uses
// "hour" DateTime; daily_pages / daily_sources use "day" Date). The
// returned args slice matches the placeholder order. Architecture Rule 8:
// site_id is the first WHERE term so the (site_id, …) ORDER BY prefix
// can prune partitions cleanly.
func whereTimeAndTenant(f *Filter, timeColumn string) (string, []any) {
	clause := fmt.Sprintf("WHERE site_id = ? AND %s >= ? AND %s < ?",
		timeColumn, timeColumn)

	return clause, []any{f.SiteID, f.From, f.To}
}

// Overview reads the headline metrics from hourly_visitors. The HLL
// states are merged across hours via uniqMerge — this is why the
// rollup uses AggregateFunction(uniqCombined64, FixedString(16)).
func (s *clickhouseStore) Overview(ctx context.Context, f *Filter) (*OverviewResult, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "hour")

	row := s.conn.QueryRow(ctx, fmt.Sprintf(`
		SELECT
			toUInt64(sum(pageviews))            AS pageviews,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue_rials))        AS revenue
		FROM statnive.hourly_visitors %s
	`, where), args...)

	var out OverviewResult
	if err := row.Scan(&out.Pageviews, &out.Visitors, &out.Goals, &out.RevenueRials); err != nil {
		return nil, fmt.Errorf("overview query: %w", err)
	}

	out.RPV = rpv(out.RevenueRials, out.Visitors)

	return &out, nil
}

// Sources reads daily_sources, GROUP BY referrer_name + channel.
// ORDER BY revenue DESC so the dashboard's primary RPV story (PLAN.md
// Project Goal #1) is the top of the table.
func (s *clickhouseStore) Sources(ctx context.Context, f *Filter) ([]SourceRow, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "day")

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			referrer_name,
			channel,
			toUInt64(sum(views))                AS views,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue_rials))        AS revenue
		FROM statnive.daily_sources %s
		GROUP BY referrer_name, channel
		ORDER BY revenue DESC, views DESC
		LIMIT ?
	`, where), append(args, f.EffectiveLimit())...)
	if err != nil {
		return nil, fmt.Errorf("sources query: %w", err)
	}
	defer rows.Close()

	var out []SourceRow

	for rows.Next() {
		var r SourceRow
		if err := rows.Scan(&r.ReferrerName, &r.Channel, &r.Views, &r.Visitors, &r.Goals, &r.RevenueRials); err != nil {
			return nil, fmt.Errorf("sources scan: %w", err)
		}

		r.RPV = rpv(r.RevenueRials, r.Visitors)
		out = append(out, r)
	}

	return out, rows.Err()
}

// Pages reads daily_pages, GROUP BY pathname.
func (s *clickhouseStore) Pages(ctx context.Context, f *Filter) ([]PageRow, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "day")

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			pathname,
			toUInt64(sum(views))                AS views,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue_rials))        AS revenue
		FROM statnive.daily_pages %s
		GROUP BY pathname
		ORDER BY views DESC
		LIMIT ?
	`, where), append(args, f.EffectiveLimit())...)
	if err != nil {
		return nil, fmt.Errorf("pages query: %w", err)
	}
	defer rows.Close()

	var out []PageRow

	for rows.Next() {
		var r PageRow
		if err := rows.Scan(&r.Pathname, &r.Views, &r.Visitors, &r.Goals, &r.RevenueRials); err != nil {
			return nil, fmt.Errorf("pages scan: %w", err)
		}

		r.RPV = rpv(r.RevenueRials, r.Visitors)
		out = append(out, r)
	}

	return out, rows.Err()
}

// SEO reads daily_sources filtered to channel = 'Organic Search' then
// rolls up by day. WITH FILL FROM .. TO .. STEP INTERVAL 1 DAY emits
// a row for every day in the requested range, even days with zero
// organic traffic — the Preact dashboard never has to fake empty
// buckets in its trend chart (doc 24 §Sec 4 pattern 8).
//
// The fill range bounds (FROM/TO) are passed as arguments alongside
// the standard whereTimeAndTenant args so clickhouse-go binds them
// positionally.
func (s *clickhouseStore) SEO(ctx context.Context, f *Filter) ([]SEORow, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "day")

	// Fill bounds: TO is exclusive in the rest of the codebase; CH's
	// WITH FILL ... TO is also exclusive, so we pass f.To unchanged.
	args = append(args, ChannelOrganicSearch, f.From, f.To)

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			day,
			toUInt64(sum(views))                AS views,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue_rials))        AS revenue
		FROM statnive.daily_sources %s AND channel = ?
		GROUP BY day
		ORDER BY day WITH FILL FROM toDate(?) TO toDate(?) STEP INTERVAL 1 DAY
	`, where), args...)
	if err != nil {
		return nil, fmt.Errorf("seo query: %w", err)
	}
	defer rows.Close()

	var out []SEORow

	for rows.Next() {
		var r SEORow
		if err := rows.Scan(&r.Day, &r.Views, &r.Visitors, &r.Goals, &r.RevenueRials); err != nil {
			return nil, fmt.Errorf("seo scan: %w", err)
		}

		out = append(out, r)
	}

	return out, rows.Err()
}

// Campaigns reads daily_sources filtered to non-empty utm_campaign,
// GROUP BY utm_campaign.
func (s *clickhouseStore) Campaigns(ctx context.Context, f *Filter) ([]CampaignRow, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "day")

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			utm_campaign,
			toUInt64(sum(views))                AS views,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue_rials))        AS revenue
		FROM statnive.daily_sources %s AND utm_campaign != ''
		GROUP BY utm_campaign
		ORDER BY revenue DESC, views DESC
		LIMIT ?
	`, where), append(args, f.EffectiveLimit())...)
	if err != nil {
		return nil, fmt.Errorf("campaigns query: %w", err)
	}
	defer rows.Close()

	var out []CampaignRow

	for rows.Next() {
		var r CampaignRow
		if err := rows.Scan(&r.UTMCampaign, &r.Views, &r.Visitors, &r.Goals, &r.RevenueRials); err != nil {
			return nil, fmt.Errorf("campaigns scan: %w", err)
		}

		r.RPV = rpv(r.RevenueRials, r.Visitors)
		out = append(out, r)
	}

	return out, rows.Err()
}

// Trend aggregates hourly_visitors to a daily series over the requested
// filter window. Powers the uPlot visitors trend on Overview + the 24h
// sparkline on Realtime. Uses WITH FILL so days with zero traffic still
// emit a row — the SPA never has to fake empty buckets.
//
// Unlike SEO, this is NOT channel-filtered: the dashboard's headline
// trend is "all traffic", not "organic only". Reads from hourly_visitors
// (not daily_pages) because daily_pages partitions by pathname, which
// would require a second SUM() pass per row.
func (s *clickhouseStore) Trend(ctx context.Context, f *Filter) ([]DailyPoint, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "hour")

	// WITH FILL bounds: TO is exclusive in the dashboard contract; CH's
	// WITH FILL ... TO is also exclusive, so pass f.To unchanged.
	args = append(args, f.From, f.To)

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			toDate(hour) AS day,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(pageviews))            AS pageviews
		FROM statnive.hourly_visitors %s
		GROUP BY day
		ORDER BY day WITH FILL FROM toDate(?) TO toDate(?) STEP INTERVAL 1 DAY
	`, where), args...)
	if err != nil {
		return nil, fmt.Errorf("trend query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var out []DailyPoint

	for rows.Next() {
		var p DailyPoint
		if err := rows.Scan(&p.Day, &p.Visitors, &p.Pageviews); err != nil {
			return nil, fmt.Errorf("trend scan: %w", err)
		}

		out = append(out, p)
	}

	return out, rows.Err()
}

// Realtime reads the latest hourly_visitors bucket. Architecture Rule 3
// forbids true 5-minute resolution; this is "active in the last hour"
// surfaced via the existing rollup.
func (s *clickhouseStore) Realtime(ctx context.Context, siteID uint32) (*RealtimeResult, error) {
	if siteID == 0 {
		return nil, fmt.Errorf("%w: site_id is required", ErrInvalidFilter)
	}

	hourStart := time.Now().UTC().Truncate(time.Hour)

	row := s.conn.QueryRow(ctx, `
		SELECT
			hour,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(pageviews))            AS pageviews
		FROM statnive.hourly_visitors
		WHERE site_id = ? AND hour >= ?
		GROUP BY hour
		ORDER BY hour DESC
		LIMIT 1
	`, siteID, hourStart)

	var out RealtimeResult
	if err := row.Scan(&out.HourUTC, &out.ActiveVisitors, &out.PageviewsLastHr); err != nil {
		// No rows for the current hour is the expected shape immediately
		// after boot or on a quiet site — return a zero-valued result
		// instead of the driver's no-rows error so handlers don't have
		// to special-case it.
		out.HourUTC = hourStart

		return &out, nil
	}

	return &out, nil
}

// Geo is reserved — the daily_geo rollup ships in v1.1.
func (s *clickhouseStore) Geo(_ context.Context, _ *Filter) ([]GeoRow, error) {
	return nil, ErrNotImplemented
}

// Devices is reserved — the daily_devices rollup ships in v1.1.
func (s *clickhouseStore) Devices(_ context.Context, _ *Filter) ([]DeviceRow, error) {
	return nil, ErrNotImplemented
}

// Funnel is reserved — windowFunnel implementation ships in v2.
func (s *clickhouseStore) Funnel(_ context.Context, _ *Filter, _ []string) (*FunnelResult, error) {
	return nil, ErrNotImplemented
}
