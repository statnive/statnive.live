package storage

import "time"

// OverviewResult is the headline metric block: total visitors, pageviews,
// goals, and revenue for a (site_id, date range). Visitors come from
// uniqMerge of HyperLogLog states (~0.5% error per CLAUDE.md).
//
// RPV is computed: revenue_rials / visitors. Returned for caller
// convenience so dashboards don't have to re-derive it.
type OverviewResult struct {
	Pageviews    uint64  `json:"pageviews"`
	Visitors     uint64  `json:"visitors"`
	Goals        uint64  `json:"goals"`
	RevenueRials uint64  `json:"revenue_rials"`
	RPV          float64 `json:"rpv_rials"`
}

// SourceRow is one row of the Sources table — referrer + channel grouped.
type SourceRow struct {
	ReferrerName string  `json:"referrer_name"`
	Channel      string  `json:"channel"`
	Views        uint64  `json:"views"`
	Visitors     uint64  `json:"visitors"`
	Goals        uint64  `json:"goals"`
	RevenueRials uint64  `json:"revenue_rials"`
	RPV          float64 `json:"rpv_rials"`
}

// PageRow is one row of the Pages table — pathname grouped.
type PageRow struct {
	Pathname     string  `json:"pathname"`
	Views        uint64  `json:"views"`
	Visitors     uint64  `json:"visitors"`
	Goals        uint64  `json:"goals"`
	RevenueRials uint64  `json:"revenue_rials"`
	RPV          float64 `json:"rpv_rials"`
}

// SEORow is one bucket of the organic-search trend — typically a daily
// series. Day is the bucket boundary (UTC); the API layer converts to
// IRST for display.
type SEORow struct {
	Day          time.Time `json:"day"`
	Views        uint64    `json:"views"`
	Visitors     uint64    `json:"visitors"`
	Goals        uint64    `json:"goals"`
	RevenueRials uint64    `json:"revenue_rials"`
}

// CampaignRow is one row of the Campaigns table — utm_campaign grouped.
type CampaignRow struct {
	UTMCampaign  string  `json:"utm_campaign"`
	Views        uint64  `json:"views"`
	Visitors     uint64  `json:"visitors"`
	Goals        uint64  `json:"goals"`
	RevenueRials uint64  `json:"revenue_rials"`
	RPV          float64 `json:"rpv_rials"`
}

// DailyPoint is one day of the all-traffic trend that feeds the uPlot
// visitors chart on Overview (and the Realtime panel's 24h sparkline).
// Distinct from SEORow because the trend here is not organic-filtered —
// callers want the total daily visitor/pageview count across all channels.
// Day is the bucket boundary in the site's IRST zone; the API layer
// converts from UTC at render time.
type DailyPoint struct {
	Day       time.Time `json:"day"`
	Visitors  uint64    `json:"visitors"`
	Pageviews uint64    `json:"pageviews"`
}

// RealtimeResult is the active-visitors widget. Backed by hourly_visitors
// for the current hour bucket — Architecture Rule 3 forbids true
// 5-minute-resolution counters in v1. Operators see "active in the last
// hour" with a 10s LRU cache so 100 dashboard tabs don't fan out to CH.
type RealtimeResult struct {
	HourUTC          time.Time `json:"hour_utc"`
	ActiveVisitors   uint64    `json:"active_visitors"`
	PageviewsLastHr  uint64    `json:"pageviews_last_hr"`
}

// GeoRow + DeviceRow are interface placeholders — Geo and Devices Store
// methods return ErrNotImplemented in v1 because the daily_geo /
// daily_devices rollups are v1.1.
type GeoRow struct {
	Province string `json:"province"`
	City     string `json:"city"`
	Visitors uint64 `json:"visitors"`
}

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
	Steps    []string  `json:"steps"`
	Counts   []uint64  `json:"counts"`
	DropOff  []float64 `json:"drop_off_pct"`
}

