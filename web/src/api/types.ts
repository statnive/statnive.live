// Mirrors internal/storage/result.go:OverviewResult. Field order doesn't
// matter for JSON decode; field NAMES are the contract. If the Go struct
// renames a json tag, the Vitest payload-golden integration test (Phase
// 7b2 contract) catches the drift.
export interface OverviewResponse {
  pageviews: number;
  visitors: number;
  goals: number;
  revenue_rials: number;
  rpv_rials: number;
}
