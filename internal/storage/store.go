package storage

import (
	"context"
	"errors"
	"time"
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
// Devices returns ErrNotImplemented in v1; it waits on the daily_devices
// rollup which ships in a separate v1.1-devices phase. Funnel returns
// ErrNotImplemented until v2 (windowFunnel).
type Store interface {
	Overview(ctx context.Context, f *Filter) (*OverviewResult, error)
	Sources(ctx context.Context, f *Filter) ([]SourceRow, error)
	SourcesByChannel(ctx context.Context, f *Filter) ([]SourceChannelRow, error)
	Pages(ctx context.Context, f *Filter) ([]PageRow, error)
	SEO(ctx context.Context, f *Filter) ([]SEORow, error)
	Campaigns(ctx context.Context, f *Filter) ([]CampaignRow, error)
	Trend(ctx context.Context, f *Filter) ([]DailyPoint, error)
	Realtime(ctx context.Context, f *Filter) (*RealtimeResult, error)

	// Geo (v1.1-geo) reads daily_geo. Drill-down rows + top-country
	// aggregate are separate methods so the handler can fan out two
	// short SELECTs against the same rollup without smearing the
	// SQL templates together.
	Geo(ctx context.Context, f *Filter) ([]GeoRow, error)
	GeoTopCountries(ctx context.Context, f *Filter) ([]GeoTopRow, error)

	// v1.1 — wait on daily_devices rollup.
	Devices(ctx context.Context, f *Filter) ([]DeviceRow, error)

	// v2 — wait on windowFunnel implementation.
	Funnel(ctx context.Context, f *Filter, steps []string) (*FunnelResult, error)

	// Segments Phase 3 — autocomplete + comparison surfaces.
	//
	// PropNames returns up to `limit` distinct prop names observed in the
	// given scope ('hit', 'session', or 'user') over the last 7 days for
	// the site. Powers /api/props/list and the dashboard's prop-filter
	// chip autocomplete. Backed by a live scan against events_raw with
	// a tight SAMPLE rate; v1.1 promotes this to a nightly-refreshed
	// prop_name_cache MV once operator traffic justifies the build.
	PropNames(ctx context.Context, f *Filter, scope string, limit int) ([]PropNameRow, error)

	// Compare pivots the variant-comparison table for an A/B style
	// experiment. dimension is a "<scope>:<name>" string (e.g.
	// "session:ab_variant"). goal is an event_name to count conversions
	// for. Returns one VariantRow per distinct value of the dimension,
	// already ranked by visitor count (so the natural control is
	// rows[0] unless the caller overrides).
	//
	// Phase 4 of segments — runs pooled-variance two-proportion z-test
	// + Wilson confidence intervals per variant vs the control row.
	// Withholds significance when any variant fails n>=100 AND k>=25
	// (Optimizely / VWO consensus); see internal/storage/segments.go.
	Compare(ctx context.Context, f *Filter, dimension, goal string) (*CompareResult, error)
}

// PropNameRow is one entry in /api/props/list. SampleValues holds up to
// 50 distinct values for the prop_name (UI uses these to populate the
// chip's value-picker autocomplete). LastSeen is the most recent
// observation time for the name; the UI sorts by it so stale prop
// names sink to the bottom of the autocomplete.
type PropNameRow struct {
	Name         string    `json:"name"`
	SampleValues []string  `json:"sample_values"`
	LastSeen     time.Time `json:"last_seen"`
}

// CompareResult is the /api/stats/compare envelope. Control is the
// variant value chosen as the reference; usually rows[0] (highest
// visitor count) unless the caller passed an explicit override.
type CompareResult struct {
	Dimension string       `json:"dimension"`
	Goal      string       `json:"goal"`
	Control   string       `json:"control"`
	Variants  []VariantRow `json:"variants"`
}

// VariantRow is one row of the variant-comparison table. Conversion
// math runs server-side so the dashboard ships zero stats logic.
//
// Nullable-by-pointer in JSON: when the sample-size guard withholds
// significance (n<100 OR conversions<25 per variant), DeltaPP, DeltaRel,
// PValue, Significant, CILow, CIHigh are all null. The UI inspects
// them and renders the warning row + "n/a" badge per § 11.4 of the plan.
type VariantRow struct {
	Value           string   `json:"value"`
	Visitors        uint64   `json:"visitors"`
	GoalCompletions uint64   `json:"goal_completions"`
	ConversionRate  float64  `json:"conversion_rate"`
	DeltaPP         *float64 `json:"delta_pp,omitempty"`
	DeltaRel        *float64 `json:"delta_rel,omitempty"`
	PValue          *float64 `json:"p_value,omitempty"`
	Significant     *bool    `json:"significant,omitempty"`
	CILow           *float64 `json:"ci_low,omitempty"`
	CIHigh          *float64 `json:"ci_high,omitempty"`
}

// ErrNotImplemented is returned by Store methods that are reserved in
// the v1 interface but won't ship until the v1.1 / v2 rollups +
// query layers exist. HTTP handlers in Phase 3b should map this to
// HTTP 501 so the dashboard renders a "coming soon" panel instead of
// a generic 500.
var ErrNotImplemented = errors.New("storage: endpoint not yet implemented in this v1 build")
