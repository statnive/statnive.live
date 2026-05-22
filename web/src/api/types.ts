// Mirrors internal/storage/result.go. Field order doesn't matter for JSON
// decode; field NAMES are the contract. If a Go struct renames a json tag,
// the Vitest payload-golden integration test catches the drift.
//
// Revenue/RPV are currency-neutral integers. The SPA's fmtMoney takes
// the active site's currency code from /api/sites and formats the
// integer as a currency-labelled string at display time only — no
// minor-unit math, no FX conversion.

export interface OverviewResponse {
  pageviews: number;
  visitors: number;
  goals: number;
  revenue: number;
  rpv: number;
}

export interface SourceRow {
  referrer_name: string;
  channel: string;
  views: number;
  visitors: number;
  goals: number;
  revenue: number;
  rpv: number;
}

// SourceChannelRow mirrors internal/storage.SourceChannelRow — the
// per-channel rollup of daily_sources used by the Sources panel's
// vertical grouped-bar chart and the channel header rows in the grouped
// table. visitors is a HLL union from the server; never derive it
// client-side by summing SourceRow.visitors.
export interface SourceChannelRow {
  channel: string;
  views: number;
  visitors: number;
  goals: number;
  revenue: number;
  rpv: number;
}

// SourcesResponse is the envelope /api/stats/sources returns: the
// per-referrer rows and the per-channel rollup, both honoring the same
// filter. The handler computes them in parallel via errgroup.
export interface SourcesResponse {
  rows: SourceRow[];
  by_channel: SourceChannelRow[];
}

export interface PageRow {
  pathname: string;
  views: number;
  visitors: number;
  goals: number;
  revenue: number;
  rpv: number;
}

export interface SEORow {
  day: string;
  views: number;
  visitors: number;
  goals: number;
  revenue: number;
}

export interface CampaignRow {
  utm_campaign: string;
  utm_source: string;
  utm_medium: string;
  utm_content: string;
  utm_term: string;
  channel: string;
  views: number;
  visitors: number;
  goals: number;
  revenue: number;
  rpv: number;
}

export interface DailyPoint {
  day: string;
  visitors: number;
  pageviews: number;
  goals: number;
  revenue: number;
}

export interface RealtimeResponse {
  hour_utc: string;
  active_visitors: number;
  pageviews_last_hr: number;
}
