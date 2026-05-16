package storage

import (
	"context"
	"errors"
)

// Store is the read-only contract every dashboard surface (HTTP handlers
// in Phase 3b, MCP tools in v2, future admin tools) reads through. The
// interface keeps the consumer types decoupled from clickhouse-go so
// tests can swap in a fake without spinning up ClickHouse.
//
// All methods MUST validate the Filter before touching ClickHouse —
// invalid input returns ErrInvalidFilter wrapped, not a misleading
// driver error. Realtime ignores f.From / f.To (it always reads the
// current hour) but still requires SiteID and respects f.Channel.
//
// Geo, Devices, and Funnel return ErrNotImplemented in v1: Geo + Devices
// wait for the daily_geo + daily_devices rollups (v1.1), Funnel waits
// for windowFunnel implementation (v2).
type Store interface {
	Overview(ctx context.Context, f *Filter) (*OverviewResult, error)
	Sources(ctx context.Context, f *Filter) ([]SourceRow, error)
	Pages(ctx context.Context, f *Filter) ([]PageRow, error)
	SEO(ctx context.Context, f *Filter) ([]SEORow, error)
	Campaigns(ctx context.Context, f *Filter) ([]CampaignRow, error)
	Trend(ctx context.Context, f *Filter) ([]DailyPoint, error)
	Realtime(ctx context.Context, f *Filter) (*RealtimeResult, error)

	// v1.1 — wait on daily_geo / daily_devices rollups.
	Geo(ctx context.Context, f *Filter) ([]GeoRow, error)
	Devices(ctx context.Context, f *Filter) ([]DeviceRow, error)

	// v2 — wait on windowFunnel implementation.
	Funnel(ctx context.Context, f *Filter, steps []string) (*FunnelResult, error)
}

// ErrNotImplemented is returned by Store methods that are reserved in
// the v1 interface but won't ship until the v1.1 / v2 rollups +
// query layers exist. HTTP handlers in Phase 3b should map this to
// HTTP 501 so the dashboard renders a "coming soon" panel instead of
// a generic 500.
var ErrNotImplemented = errors.New("storage: endpoint not yet implemented in this v1 build")
