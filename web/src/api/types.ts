// Mirrors internal/storage/result.go. Field order doesn't matter for JSON
// decode; field NAMES are the contract. If a Go struct renames a json tag,
// the Vitest payload-golden integration test catches the drift.

export interface OverviewResponse {
  pageviews: number;
  visitors: number;
  goals: number;
  revenue_rials: number;
  rpv_rials: number;
}

export interface SourceRow {
  referrer_name: string;
  channel: string;
  views: number;
  visitors: number;
  goals: number;
  revenue_rials: number;
  rpv_rials: number;
}

export interface PageRow {
  pathname: string;
  views: number;
  visitors: number;
  goals: number;
  revenue_rials: number;
  rpv_rials: number;
}

export interface SEORow {
  day: string;
  views: number;
  visitors: number;
  goals: number;
  revenue_rials: number;
}

export interface CampaignRow {
  utm_campaign: string;
  views: number;
  visitors: number;
  goals: number;
  revenue_rials: number;
  rpv_rials: number;
}

export interface DailyPoint {
  day: string;
  visitors: number;
  pageviews: number;
}

export interface RealtimeResponse {
  hour_utc: string;
  active_visitors: number;
  pageviews_last_hr: number;
}
