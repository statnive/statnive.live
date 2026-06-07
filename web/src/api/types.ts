// Re-export shim — these dashboard read types are now DERIVED from the
// committed OpenAPI contract (api/openapi.yaml → src/api/generated.ts via
// `npm run types:gen`). Do NOT hand-edit the shapes here; change the Go struct
// + api/overlay.yaml, run `make spec-build` + `npm --prefix web run types:gen`,
// and the drift gate (`types:check` + the Go TestContractInSync) stays green.
//
// Type-only (zero runtime bytes — erased at compile, bundle-gate untouched).
// Admin/MCP/auth types stay hand-mirrored in admin.ts/mcp.ts/state/auth.ts
// (their Go source is a runtime validator map, not a type — see plan §F).
import type { components } from './generated';

type S = components['schemas'];

// Result → Response renames preserve the SPA's existing interface names.
export type OverviewResponse = S['OverviewResult'];
export type RealtimeResponse = S['RealtimeResult'];

export type SourceRow = S['SourceRow'];
export type SourceChannelRow = S['SourceChannelRow'];
export type SourcesResponse = S['SourcesResponse'];
export type PageRow = S['PageRow'];
export type SEORow = S['SEORow'];
export type CampaignRow = S['CampaignRow'];
export type DailyPoint = S['DailyPoint'];
export type GeoRow = S['GeoRow'];
export type GeoTopRow = S['GeoTopRow'];
export type GeoResponse = S['GeoResponse'];
