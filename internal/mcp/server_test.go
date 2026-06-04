package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/goals"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
)

var testNow = time.Date(2026, 6, 4, 13, 0, 0, 0, time.UTC)

// --- fakes -----------------------------------------------------------------

type fakeStore struct {
	overview    *storage.OverviewResult
	trend       []storage.DailyPoint
	sources     []storage.SourceRow
	byChannel   []storage.SourceChannelRow
	pages       []storage.PageRow
	seo         []storage.SEORow
	campaigns   []storage.CampaignRow
	realtime    *storage.RealtimeResult
	geo         []storage.GeoRow
	geoTop      []storage.GeoTopRow
	props       []storage.PropNameRow
	compare     *storage.CompareResult
	eventCounts []storage.EventNameCount
	err         error
}

func (f *fakeStore) Overview(_ context.Context, _ *storage.Filter) (*storage.OverviewResult, error) {
	return f.overview, f.err
}

func (f *fakeStore) Trend(_ context.Context, _ *storage.Filter) ([]storage.DailyPoint, error) {
	return f.trend, f.err
}

func (f *fakeStore) Sources(_ context.Context, _ *storage.Filter) ([]storage.SourceRow, error) {
	return f.sources, f.err
}

func (f *fakeStore) SourcesByChannel(_ context.Context, _ *storage.Filter) ([]storage.SourceChannelRow, error) {
	return f.byChannel, f.err
}

func (f *fakeStore) Pages(_ context.Context, _ *storage.Filter) ([]storage.PageRow, error) {
	return f.pages, f.err
}

func (f *fakeStore) SEO(_ context.Context, _ *storage.Filter) ([]storage.SEORow, error) {
	return f.seo, f.err
}

func (f *fakeStore) Campaigns(_ context.Context, _ *storage.Filter) ([]storage.CampaignRow, error) {
	return f.campaigns, f.err
}

func (f *fakeStore) Realtime(_ context.Context, _ *storage.Filter) (*storage.RealtimeResult, error) {
	return f.realtime, f.err
}

// Devices + Funnel are reserved — the fake returns ErrNotImplemented so the
// not-yet-available tools/call path is exercised.
func (f *fakeStore) Devices(_ context.Context, _ *storage.Filter) ([]storage.DeviceRow, error) {
	return nil, storage.ErrNotImplemented
}

func (f *fakeStore) Funnel(_ context.Context, _ *storage.Filter, _ []string) (*storage.FunnelResult, error) {
	return nil, storage.ErrNotImplemented
}

func (f *fakeStore) Geo(_ context.Context, _ *storage.Filter) ([]storage.GeoRow, error) {
	return f.geo, f.err
}

func (f *fakeStore) GeoTopCountries(_ context.Context, _ *storage.Filter) ([]storage.GeoTopRow, error) {
	return f.geoTop, f.err
}

func (f *fakeStore) PropNames(_ context.Context, _ *storage.Filter, _ string, _ int) ([]storage.PropNameRow, error) {
	return f.props, f.err
}

func (f *fakeStore) Compare(_ context.Context, _ *storage.Filter, _, _ string) (*storage.CompareResult, error) {
	return f.compare, f.err
}

// EventNameCardinality satisfies eventAuditReader (the off-interface read).
func (f *fakeStore) EventNameCardinality(_ context.Context, _ uint32, _, _ time.Time) ([]storage.EventNameCount, error) {
	return f.eventCounts, f.err
}

// fakeGoals is a goalLister stub for the goals_list tool.
type fakeGoals struct {
	bySite map[uint32][]goals.Goal
}

func (g fakeGoals) GoalsForSite(siteID uint32) []goals.Goal { return g.bySite[siteID] }

// fakePinger is a healthChecker stub for system_health.
type fakePinger struct{ err error }

func (p fakePinger) Ping(_ context.Context) error { return p.err }

type fakeRegistry struct {
	list   []sites.Site
	byID   map[uint32]sites.SiteAdmin
	bySlug map[string]uint32
	byHost map[string]uint32
}

func (f *fakeRegistry) List(_ context.Context) ([]sites.Site, error) { return f.list, nil }

func (f *fakeRegistry) LookupSiteByID(_ context.Context, id uint32) (sites.SiteAdmin, error) {
	sa, ok := f.byID[id]
	if !ok {
		return sites.SiteAdmin{}, sites.ErrUnknownHostname
	}

	return sa, nil
}

func (f *fakeRegistry) LookupSiteIDBySlug(_ context.Context, slug string) (uint32, error) {
	id, ok := f.bySlug[slug]
	if !ok {
		return 0, sites.ErrUnknownSlug
	}

	return id, nil
}

func (f *fakeRegistry) LookupSitePolicy(_ context.Context, host string) (uint32, sites.SitePolicy, error) {
	id, ok := f.byHost[host]
	if !ok {
		return 0, sites.SitePolicy{}, sites.ErrUnknownHostname
	}

	return id, sites.SitePolicy{}, nil
}

func newTestRegistry() *fakeRegistry {
	mk := func(id uint32, host string) sites.SiteAdmin {
		return sites.SiteAdmin{Site: sites.Site{ID: id, Hostname: host, Enabled: true, TZ: "UTC", Currency: "EUR"}, Slug: host}
	}

	return &fakeRegistry{
		list:   []sites.Site{{ID: 1, Hostname: "one.test", Enabled: true, TZ: "UTC", Currency: "EUR"}, {ID: 2, Hostname: "two.test", Enabled: true, TZ: "UTC", Currency: "EUR"}},
		byID:   map[uint32]sites.SiteAdmin{1: mk(1, "one.test"), 2: mk(2, "two.test")},
		bySlug: map[string]uint32{"one": 1, "two": 2},
		byHost: map[string]uint32{"one.test": 1, "two.test": 2},
	}
}

func newTestServer(store *fakeStore) *Server {
	return New(Config{
		Store:    store,
		Concrete: store, // *fakeStore also satisfies eventAuditReader
		Health:   fakePinger{},
		Registry: newTestRegistry(),
		Goals: fakeGoals{bySite: map[uint32][]goals.Goal{
			1: {{Name: "Purchase", Pattern: "purchase", MatchType: goals.MatchTypeEventNameEquals, Value: 100, Enabled: true}},
		}},
		Version:    "test-1.2.3",
		GeoEnabled: true, // advertise the full catalog; geo-disabled has its own test
		Budget: BudgetConfig{
			CallsPerMin: 60, RowsPerMin: 20000, CallsPerSession: 2000,
			RowsPerSession: 500000, DistinctSitesPerMin: 5, WildcardFactor: 0.25,
		},
		Now: func() time.Time { return testNow },
	})
}

func call(t *testing.T, s *Server, actor *auth.User, method string, params any) *response {
	t.Helper()

	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}

		raw = b
	}

	return s.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: raw}, actor)
}

func mustCallResult(t *testing.T, resp *response) callToolResult {
	t.Helper()

	if resp == nil {
		t.Fatal("nil response")
	}

	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}

	var ct callToolResult
	if err := json.Unmarshal(resp.Result, &ct); err != nil {
		t.Fatalf("decode callToolResult: %v", err)
	}

	return ct
}

// --- tests -----------------------------------------------------------------

func TestInitialize(t *testing.T) {
	t.Parallel()

	s := newTestServer(&fakeStore{})
	resp := call(t, s, nil, "initialize", nil)

	var got struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.ProtocolVersion != mcpProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", got.ProtocolVersion, mcpProtocolVersion)
	}

	if _, ok := got.Capabilities["tools"]; !ok {
		t.Error("capabilities.tools missing")
	}

	if got.ServerInfo.Version != "test-1.2.3" {
		t.Errorf("serverInfo.version = %q", got.ServerInfo.Version)
	}

	if got.Instructions == "" {
		t.Error("serverInstructions empty")
	}
}

func TestPing(t *testing.T) {
	t.Parallel()

	resp := call(t, newTestServer(&fakeStore{}), nil, "ping", nil)
	if resp == nil || resp.Error != nil {
		t.Fatalf("ping failed: %+v", resp)
	}
}

func TestInitializedNotificationGetsNoReply(t *testing.T) {
	t.Parallel()

	s := newTestServer(&fakeStore{})
	resp := s.handle(context.Background(), request{JSONRPC: "2.0", Method: "notifications/initialized"}, nil)

	if resp != nil {
		t.Errorf("notification should get no reply, got %+v", resp)
	}
}

func TestToolsList(t *testing.T) {
	t.Parallel()

	resp := call(t, newTestServer(&fakeStore{}), wildcardActor(), "tools/list", nil)

	var got struct {
		Tools []listedTool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got.Tools) != len(catalog()) {
		t.Fatalf("tools/list returned %d tools, want %d (full catalog)", len(got.Tools), len(catalog()))
	}

	names := map[string]bool{}
	for _, tl := range got.Tools {
		names[tl.Name] = true

		if !tl.Annotations.ReadOnlyHint || tl.Annotations.DestructiveHint {
			t.Errorf("%s: annotations not read-only: %+v", tl.Name, tl.Annotations)
		}

		if len(tl.InputSchema) == 0 {
			t.Errorf("%s: missing inputSchema", tl.Name)
		}

		if tl.Meta != nil {
			t.Errorf("%s: v2 tool must not emit _meta, got %v", tl.Name, tl.Meta)
		}
	}

	for _, want := range []string{
		"list_sites", "overview", "trend", "sources", "pages", "campaigns",
		"seo", "realtime", "geo", "compare", "props_list", "goals_list",
		"my_access", "event_audit", "site_config", "about", "system_health", "devices", "funnel",
	} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestToolsList_GeoOmittedWhenDisabled(t *testing.T) {
	t.Parallel()

	// A geo-disabled deployment must NOT advertise the geo tool.
	s := New(Config{
		Store:      &fakeStore{},
		Registry:   newTestRegistry(),
		Version:    "t",
		GeoEnabled: false,
		Budget:     BudgetConfig{CallsPerMin: 100, WildcardFactor: 1},
		Now:        func() time.Time { return testNow },
	})

	resp := call(t, s, wildcardActor(), "tools/list", nil)

	var got struct {
		Tools []listedTool `json:"tools"`
	}

	_ = json.Unmarshal(resp.Result, &got)

	if len(got.Tools) != len(catalog())-1 {
		t.Errorf("geo-disabled tools/list = %d, want %d (catalog minus geo)", len(got.Tools), len(catalog())-1)
	}

	for _, tl := range got.Tools {
		if tl.Name == "geo" {
			t.Error("geo tool must be omitted when geo disabled")
		}
	}
}

func TestToolsCall_Compare(t *testing.T) {
	t.Parallel()

	store := &fakeStore{compare: &storage.CompareResult{
		Dimension: "session:ab", Goal: "purchase", Control: "A",
		Variants: []storage.VariantRow{{Value: "A", Visitors: 100}, {Value: "B", Visitors: 90}},
	}}

	resp := call(t, newTestServer(store), wildcardActor(), "tools/call", callParams{
		Name:      "compare",
		Arguments: json.RawMessage(`{"site":"1","range":"7d","dimension":"session:ab","goal":"purchase"}`),
	})

	ct := mustCallResult(t, resp)
	sc, _ := ct.StructuredContent.(map[string]any)
	variants, _ := sc["variants"].([]any)

	if len(variants) != 2 {
		t.Fatalf("compare variants = %v, want 2", sc["variants"])
	}
}

func TestToolsCall_CompareMissingArgs(t *testing.T) {
	t.Parallel()

	// dimension/goal are required → -32602 when absent.
	resp := call(t, newTestServer(&fakeStore{}), wildcardActor(), "tools/call", callParams{
		Name:      "compare",
		Arguments: json.RawMessage(`{"site":"1","range":"7d"}`),
	})

	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("compare without dimension/goal should be -32602, got %+v", resp.Error)
	}
}

func TestToolsCall_PropsList(t *testing.T) {
	t.Parallel()

	store := &fakeStore{props: []storage.PropNameRow{{Name: "plan", SampleValues: []string{"pro", "free"}}}}

	resp := call(t, newTestServer(store), wildcardActor(), "tools/call", callParams{
		Name:      "props_list",
		Arguments: json.RawMessage(`{"site":"1","scope":"session"}`),
	})

	ct := mustCallResult(t, resp)
	if arr, ok := ct.StructuredContent.([]any); !ok || len(arr) != 1 {
		t.Fatalf("props_list = %v, want 1 row", ct.StructuredContent)
	}
}

func TestToolsCall_GoalsList(t *testing.T) {
	t.Parallel()

	// newTestServer seeds one enabled goal on site 1.
	resp := call(t, newTestServer(&fakeStore{}), wildcardActor(), "tools/call", callParams{
		Name:      "goals_list",
		Arguments: json.RawMessage(`{"site":"1"}`),
	})

	ct := mustCallResult(t, resp)
	arr, ok := ct.StructuredContent.([]any)

	if !ok || len(arr) != 1 {
		t.Fatalf("goals_list = %v, want 1 goal", ct.StructuredContent)
	}

	row, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("goal row not a map: %T", arr[0])
	}

	if row["name"] != "Purchase" || row["match_type"] != "event_name_equals" {
		t.Errorf("goal summary wrong: %v", row)
	}
}

func TestToolsCall_GeoEnabled(t *testing.T) {
	t.Parallel()

	store := &fakeStore{
		geoTop: []storage.GeoTopRow{{CountryCode: "DE", Visitors: 50}},
		geo:    []storage.GeoRow{{CountryCode: "DE", City: "Berlin", Visitors: 50}},
	}

	resp := call(t, newTestServer(store), wildcardActor(), "tools/call", callParams{
		Name:      "geo",
		Arguments: json.RawMessage(`{"site":"1","range":"7d"}`),
	})

	ct := mustCallResult(t, resp)
	if ct.IsError {
		t.Fatalf("geo (enabled) should not error: %+v", ct)
	}

	sc, _ := ct.StructuredContent.(map[string]any)
	if top, _ := sc["top"].([]any); len(top) != 1 {
		t.Errorf("geo top = %v, want 1", sc["top"])
	}
}

func TestToolsCall_GeoDisabledIsNotImplemented(t *testing.T) {
	t.Parallel()

	s := New(Config{
		Store:      &fakeStore{geo: []storage.GeoRow{{CountryCode: "DE"}}},
		Registry:   newTestRegistry(),
		Version:    "t",
		GeoEnabled: false,
		Budget:     BudgetConfig{CallsPerMin: 100, WildcardFactor: 1},
		Now:        func() time.Time { return testNow },
	})

	resp := call(t, s, wildcardActor(), "tools/call", callParams{
		Name:      "geo",
		Arguments: json.RawMessage(`{"site":"1","range":"7d"}`),
	})

	ct := mustCallResult(t, resp)
	if !ct.IsError {
		t.Fatal("geo when disabled should return isError (not yet available)")
	}
}

func TestToolsCall_Sources(t *testing.T) {
	t.Parallel()

	store := &fakeStore{
		sources:   []storage.SourceRow{{ReferrerName: "google.com", Channel: "Organic Search", Visitors: 40}},
		byChannel: []storage.SourceChannelRow{{Channel: "Organic Search", Visitors: 40}},
	}

	resp := call(t, newTestServer(store), wildcardActor(), "tools/call",
		callParams{Name: "sources", Arguments: json.RawMessage(`{"site":"1","range":"7d"}`)})

	ct := mustCallResult(t, resp)
	if ct.IsError {
		t.Fatalf("unexpected isError: %+v", ct)
	}

	sc, _ := ct.StructuredContent.(map[string]any)
	rows, _ := sc["rows"].([]any)
	byCh, _ := sc["by_channel"].([]any)

	if len(rows) != 1 || len(byCh) != 1 {
		t.Fatalf("sources shape wrong: rows=%d by_channel=%d", len(rows), len(byCh))
	}
}

func TestToolsCall_DevicesNotImplemented(t *testing.T) {
	t.Parallel()

	resp := call(t, newTestServer(&fakeStore{}), wildcardActor(), "tools/call",
		callParams{Name: "devices", Arguments: json.RawMessage(`{"site":"1","range":"7d"}`)})

	ct := mustCallResult(t, resp)
	if !ct.IsError {
		t.Fatal("devices should return isError (not yet available), not a JSON-RPC error or data")
	}

	// Must be a graceful tools/call result, NOT a -32601 method-not-found.
	if resp.Error != nil {
		t.Errorf("not-implemented must be an isError result, not a JSON-RPC error: %+v", resp.Error)
	}
}

func TestToolsCall_OverviewHappyPath(t *testing.T) {
	t.Parallel()

	store := &fakeStore{overview: &storage.OverviewResult{Pageviews: 500, Visitors: 100, Goals: 3, Revenue: 250, RPV: 2.5}}
	resp := call(t, newTestServer(store), wildcardActor(), "tools/call",
		callParams{Name: "overview", Arguments: json.RawMessage(`{"site":"1","range":"7d"}`)})

	ct := mustCallResult(t, resp)
	if ct.IsError {
		t.Fatalf("unexpected isError: %+v", ct)
	}

	sc, ok := ct.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent not an object: %T", ct.StructuredContent)
	}

	if sc["visitors"] != float64(100) {
		t.Errorf("visitors = %v, want 100", sc["visitors"])
	}

	if len(ct.Content) == 0 || ct.Content[0].Text == "" {
		t.Error("missing text content block")
	}
}

func TestToolsCall_TrendRowsResult(t *testing.T) {
	t.Parallel()

	store := &fakeStore{trend: []storage.DailyPoint{{Visitors: 10}, {Visitors: 20}}}
	resp := call(t, newTestServer(store), wildcardActor(), "tools/call",
		callParams{Name: "trend", Arguments: json.RawMessage(`{"site":"one","range":"30d"}`)})

	ct := mustCallResult(t, resp)

	arr, ok := ct.StructuredContent.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("trend structuredContent = %v, want 2-element array", ct.StructuredContent)
	}
}

func TestToolsCall_UnknownTool(t *testing.T) {
	t.Parallel()

	resp := call(t, newTestServer(&fakeStore{}), wildcardActor(), "tools/call",
		callParams{Name: "delete_everything", Arguments: json.RawMessage(`{}`)})

	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("want -32602 for unknown tool, got %+v", resp.Error)
	}
}

func TestToolsCall_CrossTenantDenied(t *testing.T) {
	t.Parallel()

	// Actor scoped to site 1 only; calling site 2 must be -32602, never an
	// empty result.
	scoped := syntheticOperator([]uint32{1}, false)
	resp := call(t, newTestServer(&fakeStore{overview: &storage.OverviewResult{Visitors: 9}}), scoped, "tools/call",
		callParams{Name: "overview", Arguments: json.RawMessage(`{"site":"2","range":"7d"}`)})

	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("cross-tenant must be -32602, got %+v (result=%s)", resp.Error, resp.Result)
	}

	if resp.Result != nil {
		t.Error("cross-tenant denial must not carry a result")
	}
}

func TestToolsCall_UnknownSite(t *testing.T) {
	t.Parallel()

	resp := call(t, newTestServer(&fakeStore{}), wildcardActor(), "tools/call",
		callParams{Name: "overview", Arguments: json.RawMessage(`{"site":"999","range":"7d"}`)})

	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("unknown site must be -32602, got %+v", resp.Error)
	}
}

func TestToolsCall_StdioFailClosedByDefault(t *testing.T) {
	t.Parallel()

	// Bare stdio operator (no --allow-sites / --all-sites) reads nothing.
	failClosed := syntheticOperator(nil, false)
	resp := call(t, newTestServer(&fakeStore{overview: &storage.OverviewResult{}}), failClosed, "tools/call",
		callParams{Name: "overview", Arguments: json.RawMessage(`{"site":"1","range":"7d"}`)})

	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("fail-closed stdio must deny with -32602, got %+v", resp.Error)
	}
}

func TestToolsCall_UnknownFilterKeyRejected(t *testing.T) {
	t.Parallel()

	resp := call(t, newTestServer(&fakeStore{overview: &storage.OverviewResult{}}), wildcardActor(), "tools/call",
		callParams{Name: "overview", Arguments: json.RawMessage(`{"site":"1","filters":{"admin_override":"1"}}`)})

	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("unknown filter key must be -32602, got %+v", resp.Error)
	}
}

func TestToolsCall_BudgetExhaustion(t *testing.T) {
	t.Parallel()

	s := New(Config{
		Store:    &fakeStore{overview: &storage.OverviewResult{}},
		Registry: newTestRegistry(),
		Version:  "t",
		Budget:   BudgetConfig{CallsPerMin: 1, RowsPerMin: 100, WildcardFactor: 0.25},
		Now:      func() time.Time { return testNow },
	})

	args := callParams{Name: "overview", Arguments: json.RawMessage(`{"site":"1","range":"7d"}`)}

	first := mustCallResult(t, call(t, s, wildcardActor(), "tools/call", args))
	if first.IsError {
		t.Fatalf("first call should succeed: %+v", first)
	}

	second := mustCallResult(t, call(t, s, wildcardActor(), "tools/call", args))
	if !second.IsError {
		t.Fatal("second call should hit the budget (isError)")
	}
}

func TestListSites_FiltersByActor(t *testing.T) {
	t.Parallel()

	s := newTestServer(&fakeStore{})

	// Scoped to site 1 only → list_sites returns just site 1.
	resp := call(t, s, syntheticOperator([]uint32{1}, false), "tools/call",
		callParams{Name: "list_sites", Arguments: json.RawMessage(`{}`)})

	ct := mustCallResult(t, resp)

	arr, ok := ct.StructuredContent.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("scoped list_sites = %v, want 1 site", ct.StructuredContent)
	}

	// Wildcard → all sites.
	respAll := call(t, s, wildcardActor(), "tools/call",
		callParams{Name: "list_sites", Arguments: json.RawMessage(`{}`)})

	ctAll := mustCallResult(t, respAll)
	if arrAll, ok := ctAll.StructuredContent.([]any); !ok || len(arrAll) != 2 {
		t.Fatalf("wildcard list_sites = %v, want 2 sites", ctAll.StructuredContent)
	}
}

func wildcardActor() *auth.User {
	return syntheticOperator(nil, true)
}
