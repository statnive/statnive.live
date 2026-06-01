package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/storage"
)

// geoFake implements the Store methods the geoHandler exercises and
// counts call into each. Other methods are not used by this handler
// and panic if accidentally invoked.
type geoFake struct {
	geoCalls atomic.Int32
	topCalls atomic.Int32
	rows     []storage.GeoRow
	top      []storage.GeoTopRow
}

func (g *geoFake) Geo(_ context.Context, _ *storage.Filter) ([]storage.GeoRow, error) {
	g.geoCalls.Add(1)

	return g.rows, nil
}

func (g *geoFake) GeoTopCountries(_ context.Context, _ *storage.Filter) ([]storage.GeoTopRow, error) {
	g.topCalls.Add(1)

	return g.top, nil
}

// Unused methods — geoHandler never touches them. Panic so a test that
// accidentally hits a wrong path fails loudly.
func (*geoFake) Overview(context.Context, *storage.Filter) (*storage.OverviewResult, error) {
	panic("geoFake.Overview: handler should not reach this")
}

func (*geoFake) Sources(context.Context, *storage.Filter) ([]storage.SourceRow, error) {
	panic("geoFake.Sources")
}

func (*geoFake) SourcesByChannel(context.Context, *storage.Filter) ([]storage.SourceChannelRow, error) {
	panic("geoFake.SourcesByChannel")
}

func (*geoFake) Pages(context.Context, *storage.Filter) ([]storage.PageRow, error) {
	panic("geoFake.Pages")
}

func (*geoFake) SEO(context.Context, *storage.Filter) ([]storage.SEORow, error) {
	panic("geoFake.SEO")
}

func (*geoFake) Campaigns(context.Context, *storage.Filter) ([]storage.CampaignRow, error) {
	panic("geoFake.Campaigns")
}

func (*geoFake) Trend(context.Context, *storage.Filter) ([]storage.DailyPoint, error) {
	panic("geoFake.Trend")
}

func (*geoFake) Realtime(context.Context, *storage.Filter) (*storage.RealtimeResult, error) {
	panic("geoFake.Realtime")
}

func (*geoFake) Devices(context.Context, *storage.Filter) ([]storage.DeviceRow, error) {
	panic("geoFake.Devices")
}

func (*geoFake) Funnel(context.Context, *storage.Filter, []string) (*storage.FunnelResult, error) {
	panic("geoFake.Funnel")
}

// TestGeoHandler_FeatureFlagOff_Returns501 — when dashboard.geo_enabled
// is false, the handler short-circuits to 501 BEFORE touching the
// store. Same behavior as the pre-v1.1 binary, so the operator's
// rollback path (flip flag + SIGHUP) is a true no-op for callers.
func TestGeoHandler_FeatureFlagOff_Returns501(t *testing.T) {
	t.Parallel()

	store := &geoFake{}
	deps := newDeps(t, store)
	deps.GeoEnabled = false

	actor := actorOnSites(auth.RoleAdmin, 4)
	r := authzWith(http.MethodGet, "/api/stats/geo?site=4", actor, 4)
	w := httptest.NewRecorder()
	geoHandler(deps)(w, r)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d; want 501. body = %q", w.Code, w.Body.String())
	}

	if got := store.geoCalls.Load(); got != 0 {
		t.Errorf("Store.Geo called %d times; want 0 when flag off", got)
	}

	if got := store.topCalls.Load(); got != 0 {
		t.Errorf("Store.GeoTopCountries called %d times; want 0 when flag off", got)
	}
}

// TestGeoHandler_FeatureFlagOn_ReturnsCombinedEnvelope — when the flag
// is on, both store methods run (in parallel via errgroup) and the
// response wraps them as {"top": [...], "rows": [...]}.
func TestGeoHandler_FeatureFlagOn_ReturnsCombinedEnvelope(t *testing.T) {
	t.Parallel()

	store := &geoFake{
		rows: []storage.GeoRow{
			{CountryCode: "IR", Province: "Tehran", City: "Tehran", Views: 120, Visitors: 80, Revenue: 4_000_000},
			{CountryCode: "US", Province: "California", City: "San Francisco", Views: 25, Visitors: 20, Revenue: 25_000_000},
		},
		top: []storage.GeoTopRow{
			{CountryCode: "US", Visitors: 20, Revenue: 25_000_000},
			{CountryCode: "IR", Visitors: 80, Revenue: 4_000_000},
		},
	}

	deps := newDeps(t, store)
	deps.GeoEnabled = true

	actor := actorOnSites(auth.RoleAdmin, 4)
	r := authzWith(http.MethodGet, "/api/stats/geo?site=4", actor, 4)
	w := httptest.NewRecorder()
	geoHandler(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200. body = %q", w.Code, w.Body.String())
	}

	if got := store.geoCalls.Load(); got != 1 {
		t.Errorf("Store.Geo called %d times; want 1", got)
	}

	if got := store.topCalls.Load(); got != 1 {
		t.Errorf("Store.GeoTopCountries called %d times; want 1", got)
	}

	var got struct {
		Top  []storage.GeoTopRow `json:"top"`
		Rows []storage.GeoRow    `json:"rows"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v; body = %q", err, w.Body.String())
	}

	if len(got.Top) != 2 || got.Top[0].CountryCode != "US" {
		t.Errorf("top payload mismatch: %+v", got.Top)
	}

	if len(got.Rows) != 2 || got.Rows[0].CountryCode != "IR" {
		t.Errorf("rows payload mismatch: %+v", got.Rows)
	}
}
