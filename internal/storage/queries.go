package storage

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"
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

// trimCountryCode strips clickhouse-go's `FixedString(2)` padding off
// the country_code column. Centralized here so a future GeoIP encoding
// change is a single edit, not a grep across Store methods.
func trimCountryCode(cc string) string {
	return strings.TrimSpace(strings.Trim(cc, "\x00"))
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

// appendPropPredicates appends scoped Map[String,String] equality
// predicates to a WHERE clause + args slice and returns the result.
// Each predicate emits TWO parameterised placeholders so clickhouse-go
// binds the prop name and value as separate parameters (defense
// against any future SQL-injection seam — names + values arrive from
// untrusted URL query strings).
//
// Used by the raw-fallback query path (Filter.HasPropFilter() == true)
// against the events_raw Map columns introduced by migration 020. The
// rollup path does not call this — rollup tables have no Map columns.
//
// The function intentionally iterates in key-sorted order so two
// callers building the same filter generate byte-identical SQL.
func appendPropPredicates(where string, args []any, scope string, props map[string]string) (string, []any) {
	if len(props) == 0 {
		return where, args
	}

	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		where += fmt.Sprintf(" AND has(%s, ?) AND %s[?] = ?", scope, scope)
		args = append(args, k, k, props[k])
	}

	return where, args
}

// whereWithProps wraps whereTimeAndTenant + applyFilters and appends
// the three scoped prop predicates. Used by the raw-fallback handlers
// when Filter.HasPropFilter() == true.
//
// Architecture Rule 8 invariant: whereTimeAndTenant is still the
// first call, so site_id pruning runs before any prop check.
func whereWithProps(f *Filter, timeColumn string, cols map[string]bool) (string, []any) {
	where, args := whereTimeAndTenant(f, timeColumn)
	where, args = applyFilters(f, where, args, cols)

	where, args = appendPropPredicates(where, args, "hit_props", f.HitProps)
	where, args = appendPropPredicates(where, args, "session_props", f.SessionProps)
	where, args = appendPropPredicates(where, args, "user_props", f.UserProps)

	return where, args
}

// applyFilters extends a base WHERE clause with rollup-supported filter
// dimensions. Only fields whose column lives on the target rollup are
// appended — v1 rollups lack device / country / browser / OS columns
// (those ship in v1.1 with daily_devices + daily_geo), so passing
// f.Device here is a no-op. v1.1 will replace this with table-aware
// filter routing once the enriched rollups exist.
//
// Supported today:
//   - channel (LowCardinality, on daily_sources)
//   - referrer_name (LowCardinality, on daily_sources)
//   - utm_source, utm_medium, utm_campaign (LowCardinality / String, on daily_sources)
//   - pathname (String, on daily_pages — matched via equality, not LIKE,
//     since LowCardinality + LIKE is a bad combination at SamplePlatform scale)
//
// The `cols` set lets each query opt in only to the columns its rollup
// actually has — passing "pathname" for a daily_sources query would
// produce a SQL error.
func applyFilters(f *Filter, where string, args []any, cols map[string]bool) (string, []any) {
	if f == nil {
		return where, args
	}

	candidates := []struct {
		column string
		value  string
	}{
		{"channel", f.Channel},
		{"referrer_name", f.Referrer},
		{"utm_source", f.UTMSource},
		{"utm_medium", f.UTMMedium},
		{"utm_campaign", f.UTMCampaign},
		{"utm_content", f.UTMContent},
		{"utm_term", f.UTMTerm},
		{"pathname", f.Path},
		{"country_code", f.Country},
	}

	for _, c := range candidates {
		if !cols[c.column] || c.value == "" {
			continue
		}

		where += " AND " + c.column + " = ?"
		args = append(args, c.value)
	}

	return where, args
}

// rollup column sets — passed to applyFilters so each query only
// attempts dimensions its target table actually has. Migration 015 added
// channel to hourly_visitors + daily_pages; queries over those tables
// can now narrow by channel the same way Sources / Campaigns already do.
var (
	dailySourcesCols = map[string]bool{
		"channel":       true,
		"referrer_name": true,
		"utm_source":    true,
		"utm_medium":    true,
		"utm_campaign":  true,
		"utm_content":   true,
		"utm_term":      true,
	}
	dailyPagesCols = map[string]bool{
		"pathname": true,
		"channel":  true,
	}
	hourlyVisitorsCols = map[string]bool{
		"channel": true,
	}
	// daily_geo carries only the three geo dimensions — no channel /
	// referrer / utm columns on this rollup, by design (v1.1-geo: keep
	// the rollup narrow; cross-dimensional filters route through the
	// Sources / Pages panels instead).
	dailyGeoCols = map[string]bool{
		"country_code": true,
	}

	// Whitelist values MUST be bare columns or expressions valid in an
	// outer ORDER BY against the SELECT list — no aggregates, no AS
	// aliases. orderClause appends " DESC"/" ASC" to each top-level
	// comma-separated term and would emit garbage SQL for `sum(x) AS y`.
	metricsSortable = map[string]string{
		"views":    "views",
		"visitors": "visitors",
		"goals":    "goals",
		"revenue":  "revenue, visitors",
		"rpv":      "if(visitors > 0, revenue / visitors, 0)",
	}

	pagesSortable = sortableExtending(metricsSortable, map[string]string{
		"pathname": "pathname",
	})
	sourcesSortable = sortableExtending(metricsSortable, map[string]string{
		"referrer": "referrer_name",
		"channel":  "channel",
	})
	campaignsSortable = sortableExtending(metricsSortable, map[string]string{
		"campaign": "utm_campaign",
	})
	// SourcesByChannel groups by channel only; no referrer_name in SELECT.
	// A `sort=referrer` from the Sources table is ignored here (falls back
	// to the default) — the per-referrer detail rows still pick it up via
	// sourcesSortable.
	sourcesByChannelSortable = sortableExtending(metricsSortable, map[string]string{
		"channel": "channel",
	})
	// geoSortable extends the shared metric sorts with the three geo
	// dimensions so the Sources-style sort=country chip works.
	geoSortable = sortableExtending(metricsSortable, map[string]string{
		"country":  "country_code",
		"province": "province",
		"city":     "city",
	})
)

// sortableExtending returns a copy of base with extras merged on top —
// the canonical way to compose per-query sort whitelists from the shared
// metricsSortable base.
func sortableExtending(base, extras map[string]string) map[string]string {
	out := maps.Clone(base)
	for k, v := range extras {
		out[k] = v
	}

	return out
}

// orderClause renders ORDER BY from f.Sort using the per-query whitelist;
// compound expressions get f.Dir applied to each top-level term. Falls back
// to the hardcoded default when f.Sort is empty or not whitelisted.
func orderClause(f *Filter, allowed map[string]string, fallback string) string {
	expr, ok := allowed[f.Sort]
	if !ok {
		return "ORDER BY " + fallback
	}

	dir := "DESC"
	if f.Dir == "asc" {
		dir = "ASC"
	}

	parts := splitTopLevelCommas(expr)
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p) + " " + dir
	}

	return "ORDER BY " + strings.Join(parts, ", ")
}

// splitTopLevelCommas splits s on commas that sit at paren depth 0 only —
// so `if(visitors > 0, revenue / visitors, 0)` stays one term, while
// `revenue, visitors` splits into two. Required because orderClause
// appends " DESC"/" ASC" to each term and would otherwise corrupt nested
// function calls (e.g. `if(... DESC, revenue / visitors DESC, 0) DESC`).
func splitTopLevelCommas(s string) []string {
	var out []string

	depth, start := 0, 0

	for i := range len(s) {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}

	return append(out, s[start:])
}

// Overview reads the headline metrics from hourly_visitors. The HLL
// states are merged across hours via uniqMerge — this is why the
// rollup uses AggregateFunction(uniqCombined64, FixedString(16)).
//
// Migration 015 added channel to hourly_visitors, so applyFilters can
// now narrow the KPI tiles to a single channel chip when the operator
// asks for it. Empty f.Channel is a no-op.
func (s *clickhouseStore) Overview(ctx context.Context, f *Filter) (*OverviewResult, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "hour")
	where, args = applyFilters(f, where, args, hourlyVisitorsCols)

	row := s.conn.QueryRow(ctx, fmt.Sprintf(`
		SELECT
			toUInt64(sum(pageviews))            AS pageviews,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue))              AS revenue
		FROM statnive.hourly_visitors %s
	`, where), args...)

	var out OverviewResult
	if err := row.Scan(&out.Pageviews, &out.Visitors, &out.Goals, &out.Revenue); err != nil {
		return nil, fmt.Errorf("overview query: %w", err)
	}

	out.RPV = rpv(out.Revenue, out.Visitors)

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
	where, args = applyFilters(f, where, args, dailySourcesCols)

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			referrer_name,
			channel,
			toUInt64(sum(views))                AS views,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue))              AS revenue
		FROM statnive.daily_sources %s
		GROUP BY referrer_name, channel
		%s
		LIMIT ?
	`, where, orderClause(f, sourcesSortable, "revenue DESC, views DESC")), append(args, f.EffectiveLimit())...)
	if err != nil {
		return nil, fmt.Errorf("sources query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []SourceRow{}

	for rows.Next() {
		var r SourceRow
		if err := rows.Scan(&r.ReferrerName, &r.Channel, &r.Views, &r.Visitors, &r.Goals, &r.Revenue); err != nil {
			return nil, fmt.Errorf("sources scan: %w", err)
		}

		r.RPV = rpv(r.Revenue, r.Visitors)
		out = append(out, r)
	}

	return out, rows.Err()
}

// SourcesByChannel reads daily_sources, GROUP BY channel only. Powers the
// per-channel grouped-bar chart and the channel header rows in the
// grouped Sources table. Sort order matches Sources (revenue DESC, views
// DESC) so the chart and table align row-by-row.
//
// Visitors come from a server-side uniqCombined64Merge across the HLL
// states — never a client-side sum of per-referrer visitor counts (HLL
// union is sub-additive when visitors overlap across referrers).
//
// daily_sources is ORDER BY (site_id, day, channel, referrer_name, ...);
// this query's GROUP BY channel hits a 3-column index prefix and uses
// stream aggregation, making it faster than the per-referrer Sources
// query above. No LIMIT — channel cardinality is bounded (~8 in v1).
func (s *clickhouseStore) SourcesByChannel(ctx context.Context, f *Filter) ([]SourceChannelRow, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "day")
	where, args = applyFilters(f, where, args, dailySourcesCols)

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			channel,
			toUInt64(sum(views))                AS views,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue))              AS revenue
		FROM statnive.daily_sources %s
		GROUP BY channel
		%s
	`, where, orderClause(f, sourcesByChannelSortable, "revenue DESC, views DESC")), args...)
	if err != nil {
		return nil, fmt.Errorf("sources_by_channel query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []SourceChannelRow{}

	for rows.Next() {
		var r SourceChannelRow
		if err := rows.Scan(&r.Channel, &r.Views, &r.Visitors, &r.Goals, &r.Revenue); err != nil {
			return nil, fmt.Errorf("sources_by_channel scan: %w", err)
		}

		r.RPV = rpv(r.Revenue, r.Visitors)
		out = append(out, r)
	}

	return out, rows.Err()
}

// Pages reads daily_pages, GROUP BY pathname. Pages + Campaigns share
// SELECT shape but target different rollup tables with different column
// typing; extracting a helper would erase the rollup/column coupling the
// skill enforces per Architecture Rule 8.
func (s *clickhouseStore) Pages(ctx context.Context, f *Filter) ([]PageRow, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "day")
	where, args = applyFilters(f, where, args, dailyPagesCols)

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			pathname,
			toUInt64(sum(views))                AS views,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue))              AS revenue
		FROM statnive.daily_pages %s
		GROUP BY pathname
		%s
		LIMIT ?
	`, where, orderClause(f, pagesSortable, "views DESC")), append(args, f.EffectiveLimit())...)
	if err != nil {
		return nil, fmt.Errorf("pages query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []PageRow{}

	for rows.Next() {
		var r PageRow
		if err := rows.Scan(&r.Pathname, &r.Views, &r.Visitors, &r.Goals, &r.Revenue); err != nil {
			return nil, fmt.Errorf("pages scan: %w", err)
		}

		r.RPV = rpv(r.Revenue, r.Visitors)
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
	// SEO is channel-locked to Organic Search by definition, so skip
	// the channel key from applyFilters — let the other dimensions
	// (referrer_name for search engine slice, utm_* for tagged organic)
	// narrow further.
	seoCols := map[string]bool{
		"referrer_name": true,
		"utm_source":    true,
		"utm_medium":    true,
		"utm_campaign":  true,
	}
	where, args = applyFilters(f, where, args, seoCols)

	// Fill bounds: TO is exclusive in the rest of the codebase; CH's
	// WITH FILL ... TO is also exclusive, so we pass f.To unchanged.
	args = append(args, ChannelOrganicSearch, f.From, f.To)

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			day,
			toUInt64(sum(views))                AS views,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue))              AS revenue
		FROM statnive.daily_sources %s AND channel = ?
		GROUP BY day
		ORDER BY day WITH FILL FROM toDate(?) TO toDate(?) STEP INTERVAL 1 DAY
	`, where), args...)
	if err != nil {
		return nil, fmt.Errorf("seo query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []SEORow{}

	for rows.Next() {
		var r SEORow
		if err := rows.Scan(&r.Day, &r.Views, &r.Visitors, &r.Goals, &r.Revenue); err != nil {
			return nil, fmt.Errorf("seo scan: %w", err)
		}

		out = append(out, r)
	}

	return out, rows.Err()
}

// Campaigns reads daily_sources filtered to non-empty utm_campaign and
// GROUPs BY the full UTM tuple (campaign / source / medium / content /
// term). Migration 016 added utm_content + utm_term to the rollup so
// the breakdown can render the four UTM dims the operator tracker tags
// emit. The SPA folds the flat row set into a Campaign → Source →
// Medium → Content tree client-side. Pre-016 historical rows read
// utm_content=” and utm_term=” — they collapse into a single
// "(none)" leaf which is the correct semantic for untagged dims.
func (s *clickhouseStore) Campaigns(ctx context.Context, f *Filter) ([]CampaignRow, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "day")
	where, args = applyFilters(f, where, args, dailySourcesCols)

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			utm_campaign,
			utm_source,
			utm_medium,
			utm_content,
			utm_term,
			channel,
			toUInt64(sum(views))                AS views,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue))              AS revenue
		FROM statnive.daily_sources %s AND utm_campaign != ''
		GROUP BY utm_campaign, utm_source, utm_medium, utm_content, utm_term, channel
		%s
		LIMIT ?
	`, where, orderClause(f, campaignsSortable, "revenue DESC, views DESC")), append(args, f.EffectiveLimit())...)
	if err != nil {
		return nil, fmt.Errorf("campaigns query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []CampaignRow{}

	for rows.Next() {
		var r CampaignRow
		if err := rows.Scan(
			&r.UTMCampaign,
			&r.UTMSource,
			&r.UTMMedium,
			&r.UTMContent,
			&r.UTMTerm,
			&r.Channel,
			&r.Views,
			&r.Visitors,
			&r.Goals,
			&r.Revenue,
		); err != nil {
			return nil, fmt.Errorf("campaigns scan: %w", err)
		}

		r.RPV = rpv(r.Revenue, r.Visitors)
		out = append(out, r)
	}

	return out, rows.Err()
}

// Trend aggregates hourly_visitors to a daily series over the requested
// filter window. Powers the uPlot visitors trend on Overview + the 24h
// sparkline on Realtime. Uses WITH FILL so days with zero traffic still
// emit a row — the SPA never has to fake empty buckets.
//
// Default behaviour is "all traffic" — applyFilters is a no-op when
// f.Channel is empty. When a channel chip is active, the trend narrows
// to that channel (migration 015 added the column). Reads from
// hourly_visitors (not daily_pages) because daily_pages partitions by
// pathname, which would require a second SUM() pass per row.
func (s *clickhouseStore) Trend(ctx context.Context, f *Filter) ([]DailyPoint, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "hour")
	where, args = applyFilters(f, where, args, hourlyVisitorsCols)

	// WITH FILL bounds: TO is exclusive in the dashboard contract; CH's
	// WITH FILL ... TO is also exclusive, so pass f.To unchanged.
	args = append(args, f.From, f.To)

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			toDate(hour) AS day,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(pageviews))            AS pageviews,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue))              AS revenue
		FROM statnive.hourly_visitors %s
		GROUP BY day
		ORDER BY day WITH FILL FROM toDate(?) TO toDate(?) STEP INTERVAL 1 DAY
	`, where), args...)
	if err != nil {
		return nil, fmt.Errorf("trend query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []DailyPoint{}

	for rows.Next() {
		var p DailyPoint
		if err := rows.Scan(&p.Day, &p.Visitors, &p.Pageviews, &p.Goals, &p.Revenue); err != nil {
			return nil, fmt.Errorf("trend scan: %w", err)
		}

		out = append(out, p)
	}

	return out, rows.Err()
}

// Realtime reads the latest hourly_visitors bucket. Architecture Rule 3
// forbids true 5-minute resolution; this is "active in the last hour"
// surfaced via the existing rollup.
//
// Filter contract: SiteID is required; Channel narrows the bucket to a
// single channel when set (migration 015). From/To on the filter are
// ignored — Realtime is always "current hour", so the time predicate is
// computed here rather than read from f. Validate() still runs so a
// caller that hand-builds a Filter for Realtime gets the same shape
// errors (nil filter, zero SiteID, bad range) as every other method.
func (s *clickhouseStore) Realtime(ctx context.Context, f *Filter) (*RealtimeResult, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	hourStart := time.Now().UTC().Truncate(time.Hour)

	where := "WHERE site_id = ? AND hour >= ?"
	args := []any{f.SiteID, hourStart}

	if f.Channel != "" {
		where += " AND channel = ?"

		args = append(args, f.Channel)
	}

	row := s.conn.QueryRow(ctx, fmt.Sprintf(`
		SELECT
			hour,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(pageviews))            AS pageviews
		FROM statnive.hourly_visitors
		%s
		GROUP BY hour
		ORDER BY hour DESC
		LIMIT 1
	`, where), args...)

	var out RealtimeResult
	if err := row.Scan(&out.HourUTC, &out.ActiveVisitors, &out.PageviewsLastHr); err != nil {
		// No rows for the current hour is the expected shape immediately
		// after boot or on a quiet site — return a zero-valued result
		// instead of the driver's no-rows error so handlers don't have
		// to special-case it.
		out.HourUTC = hourStart

		return &out, nil //nolint:nilerr // no-rows on a quiet site is the expected shape
	}

	return &out, nil
}

// geoTopCountriesLimit caps GeoTopCountries server-side. The dashboard
// slices to top-10 per axis (visitors / revenue) and rolls everything
// past the 10th into an "Other" slice in the pie. 25 is the smallest
// constant that always covers both top-10 lists even when the two
// rankings overlap minimally; bumping it costs nothing because the
// outer LIMIT runs after the GROUP BY.
const geoTopCountriesLimit = 25

// Geo reads daily_geo, GROUP BY (country_code, province, city). Drives
// the panel's drill-down table; the headline + pie come from
// GeoTopCountries below. ORDER BY revenue DESC, visitors DESC matches
// Sources / Campaigns' RPV-first sort (CLAUDE.md Project Goal #1).
//
// applyFilters keeps the filter surface bounded to dimensions that
// actually live on daily_geo (country_code only — province / city are
// drill-down dimensions, not chips). Other filter fields are silently
// ignored here, mirroring the Sources behavior for path / device.
func (s *clickhouseStore) Geo(ctx context.Context, f *Filter) ([]GeoRow, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "day")
	where, args = applyFilters(f, where, args, dailyGeoCols)

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			country_code,
			province,
			city,
			toUInt64(sum(views))                AS views,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue))              AS revenue
		FROM statnive.daily_geo %s
		GROUP BY country_code, province, city
		%s
		LIMIT ?
	`, where, orderClause(f, geoSortable, "revenue DESC, visitors DESC")), append(args, f.EffectiveLimit())...)
	if err != nil {
		return nil, fmt.Errorf("geo query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []GeoRow{}

	for rows.Next() {
		var (
			r  GeoRow
			cc string
		)

		if err := rows.Scan(&cc, &r.Province, &r.City, &r.Views, &r.Visitors, &r.Goals, &r.Revenue); err != nil {
			return nil, fmt.Errorf("geo scan: %w", err)
		}

		r.CountryCode = trimCountryCode(cc)
		r.RPV = rpv(r.Revenue, r.Visitors)
		out = append(out, r)
	}

	return out, rows.Err()
}

// GeoTopCountries reads daily_geo, GROUP BY country_code only — the
// country-level aggregate that drives the "Top 10 by Visitors / Top 10
// by Revenue" headline + the share-of-visitors donut. Capped at 25
// rows (geoTopCountriesLimit); the panel sorts client-side on both
// axes and collapses everything past the 10th into "Other" in the pie.
func (s *clickhouseStore) GeoTopCountries(ctx context.Context, f *Filter) ([]GeoTopRow, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}

	where, args := whereTimeAndTenant(f, "day")
	where, args = applyFilters(f, where, args, dailyGeoCols)

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT
			country_code,
			toUInt64(sum(views))                AS views,
			toUInt64(uniqCombined64Merge(visitors_state)) AS visitors,
			toUInt64(sum(goals))                AS goals,
			toUInt64(sum(revenue))              AS revenue
		FROM statnive.daily_geo %s
		GROUP BY country_code
		ORDER BY visitors DESC
		LIMIT ?
	`, where), append(args, uint32(geoTopCountriesLimit))...)
	if err != nil {
		return nil, fmt.Errorf("geo_top_countries query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []GeoTopRow{}

	for rows.Next() {
		var (
			r  GeoTopRow
			cc string
		)

		if err := rows.Scan(&cc, &r.Views, &r.Visitors, &r.Goals, &r.Revenue); err != nil {
			return nil, fmt.Errorf("geo_top_countries scan: %w", err)
		}

		r.CountryCode = trimCountryCode(cc)
		r.RPV = rpv(r.Revenue, r.Visitors)
		out = append(out, r)
	}

	return out, rows.Err()
}

// Devices is reserved — the daily_devices rollup ships in v1.1.
func (s *clickhouseStore) Devices(_ context.Context, _ *Filter) ([]DeviceRow, error) {
	return nil, ErrNotImplemented
}

// Funnel is reserved — windowFunnel implementation ships in v2.
func (s *clickhouseStore) Funnel(_ context.Context, _ *Filter, _ []string) (*FunnelResult, error) {
	return nil, ErrNotImplemented
}
