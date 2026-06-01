package storage

import "time"

// OverviewResult is the headline metric block: total visitors, pageviews,
// goals, and revenue for a (site_id, date range). Visitors come from
// uniqMerge of HyperLogLog states (~0.5% error per CLAUDE.md).
//
// Revenue is the integer aggregate; RPV is revenue / visitors. Both
// are currency-neutral integers — the SPA's fmtMoney formats them
// using the active site's currency code (display label only, no
// minor-unit math).
type OverviewResult struct {
	Pageviews uint64  `json:"pageviews"`
	Visitors  uint64  `json:"visitors"`
	Goals     uint64  `json:"goals"`
	Revenue   uint64  `json:"revenue"`
	RPV       float64 `json:"rpv"`
}

// SourceRow is one row of the Sources table — referrer + channel grouped.
type SourceRow struct {
	ReferrerName string  `json:"referrer_name"`
	Channel      string  `json:"channel"`
	Views        uint64  `json:"views"`
	Visitors     uint64  `json:"visitors"`
	Goals        uint64  `json:"goals"`
	Revenue      uint64  `json:"revenue"`
	RPV          float64 `json:"rpv"`
}

// SourceChannelRow is one row of the per-channel rollup that powers the
// Sources panel's vertical grouped-bar chart and the channel header rows
// in the grouped table. Same shape as SourceRow minus ReferrerName.
// Visitors MUST come from a server-side uniqCombined64Merge — summing
// per-referrer visitor counts over-counts because of HLL union
// sub-additivity when visitors overlap across referrers.
type SourceChannelRow struct {
	Channel  string  `json:"channel"`
	Views    uint64  `json:"views"`
	Visitors uint64  `json:"visitors"`
	Goals    uint64  `json:"goals"`
	Revenue  uint64  `json:"revenue"`
	RPV      float64 `json:"rpv"`
}

// PageRow is one row of the Pages table — pathname grouped.
type PageRow struct {
	Pathname string  `json:"pathname"`
	Views    uint64  `json:"views"`
	Visitors uint64  `json:"visitors"`
	Goals    uint64  `json:"goals"`
	Revenue  uint64  `json:"revenue"`
	RPV      float64 `json:"rpv"`
}

// SEORow is one bucket of the organic-search trend — typically a daily
// series. Day is the bucket boundary (UTC); the API layer converts to
// the site's TZ for display.
type SEORow struct {
	Day      time.Time `json:"day"`
	Views    uint64    `json:"views"`
	Visitors uint64    `json:"visitors"`
	Goals    uint64    `json:"goals"`
	Revenue  uint64    `json:"revenue"`
}

// CampaignRow is one row of the Campaigns breakdown — the full UTM tuple
// (campaign / source / medium / content / term) plus the enriched
// channel attribution, grouped. The SPA folds the flat row set into a
// Campaign → Source → Medium → Content tree at render time and
// re-aggregates by channel for the donut chart strip. Channel comes
// from the 17-step mapper in internal/enrich/channel.go and stays on
// daily_sources rows (migration 002 ORDER BY). Empty strings on any
// UTM field mean "no value tracked"; the SPA renders those as "(none)"
// so untagged traffic still shows up in the rollup totals.
type CampaignRow struct {
	UTMCampaign string  `json:"utm_campaign"`
	UTMSource   string  `json:"utm_source"`
	UTMMedium   string  `json:"utm_medium"`
	UTMContent  string  `json:"utm_content"`
	UTMTerm     string  `json:"utm_term"`
	Channel     string  `json:"channel"`
	Views       uint64  `json:"views"`
	Visitors    uint64  `json:"visitors"`
	Goals       uint64  `json:"goals"`
	Revenue     uint64  `json:"revenue"`
	RPV         float64 `json:"rpv"`
}

// DailyPoint is one day of the all-traffic trend. Distinct from SEORow
// because this series is not organic-filtered. Day is the bucket
// boundary in the site's IRST zone; the API layer converts from UTC at
// render time. Conversion and RPV are derived per-day from these raw
// counts in the SPA so they share the divide-by-zero rule with the
// headline KPI tiles.
type DailyPoint struct {
	Day       time.Time `json:"day"`
	Visitors  uint64    `json:"visitors"`
	Pageviews uint64    `json:"pageviews"`
	Goals     uint64    `json:"goals"`
	Revenue   uint64    `json:"revenue"`
}

// RealtimeResult is the active-visitors widget. Backed by hourly_visitors
// for the current hour bucket — Architecture Rule 3 forbids true
// 5-minute-resolution counters in v1. Operators see "active in the last
// hour" with a 10s LRU cache so 100 dashboard tabs don't fan out to CH.
type RealtimeResult struct {
	HourUTC         time.Time `json:"hour_utc"`
	ActiveVisitors  uint64    `json:"active_visitors"`
	PageviewsLastHr uint64    `json:"pageviews_last_hr"`
}

// GeoRow is one (country, province, city) tuple of the daily_geo rollup.
// The dashboard panel folds the flat row set into a country → province →
// city tree at render time; empty province / city + sentinel "--"
// country are rendered as "Unknown".
type GeoRow struct {
	CountryCode string  `json:"country_code"`
	Province    string  `json:"province"`
	City        string  `json:"city"`
	Views       uint64  `json:"views"`
	Visitors    uint64  `json:"visitors"`
	Goals       uint64  `json:"goals"`
	Revenue     uint64  `json:"revenue"`
	RPV         float64 `json:"rpv"`
}

// GeoTopRow is one country aggregate that drives the panel's "Top 10 by
// Visitors / Top 10 by Revenue" headline block and the share-of-visitors
// donut. Distinct from GeoRow so the headline doesn't have to re-aggregate
// across province / city on the client. Capped at 25 rows server-side.
type GeoTopRow struct {
	CountryCode string  `json:"country_code"`
	Views       uint64  `json:"views"`
	Visitors    uint64  `json:"visitors"`
	Goals       uint64  `json:"goals"`
	Revenue     uint64  `json:"revenue"`
	RPV         float64 `json:"rpv"`
}

// DeviceRow is one row of the daily_devices rollup (v1.1 placeholder).
type DeviceRow struct {
	Device   string `json:"device"`
	Browser  string `json:"browser"`
	OS       string `json:"os"`
	Visitors uint64 `json:"visitors"`
}

// FunnelResult mirrors what windowFunnel will return in v2: per-step
// counts + drop-off percentages. Reserved here so the Store interface
// is stable across the v2 swap.
type FunnelResult struct {
	Steps   []string  `json:"steps"`
	Counts  []uint64  `json:"counts"`
	DropOff []float64 `json:"drop_off_pct"`
}
