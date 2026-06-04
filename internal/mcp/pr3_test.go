package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/storage"
)

// apiTokenActor is a site-bound API-token principal (the shape
// auth.APITokenMiddleware produces): UserID==Nil, a single SiteID, Role=api.
func apiTokenActor(siteID uint32) *auth.User {
	return &auth.User{UserID: uuid.Nil, SiteID: siteID, Role: auth.RoleAPI}
}

// TestAdminTools_DeniedForAPIRole is the marquee permission test: an api-role
// actor (the role every HTTP Bearer gets) is denied on the admin tools with
// -32602 — matching the REST API's 403 — while STILL able to call the
// analytics tools. Without the role floor, MCP would be a wider door than the
// REST surface it wraps.
func TestAdminTools_DeniedForAPIRole(t *testing.T) {
	t.Parallel()

	store := &fakeStore{
		overview:    &storage.OverviewResult{Visitors: 1},
		eventCounts: []storage.EventNameCount{{Name: "purchase", Count: 5}},
	}
	s := newTestServer(store)

	// api-role token bound to site 1 (so the per-site gate would pass — the
	// ROLE floor is what must reject).
	api := apiTokenActor(1)

	for _, tool := range []string{"event_audit", "site_config"} {
		resp := call(t, s, api, "tools/call", callParams{
			Name:      tool,
			Arguments: json.RawMessage(`{"site":"1","range":"7d"}`),
		})

		if resp.Error == nil || resp.Error.Code != codeInvalidParams {
			t.Errorf("%s with api role should be -32602 (insufficient role), got %+v", tool, resp.Error)
		}
	}

	// Same api token CAN read analytics (overview) on its bound site.
	ok := mustCallResult(t, call(t, s, api, "tools/call", callParams{
		Name:      "overview",
		Arguments: json.RawMessage(`{"site":"1","range":"7d"}`),
	}))
	if ok.IsError {
		t.Errorf("api role should be allowed on overview (analytics), got isError: %+v", ok)
	}
}

func TestToolsCall_EventAudit_CapStatus(t *testing.T) {
	t.Parallel()

	// 2 distinct event names → under the CNIL 3-event ceiling → "ok".
	under := newTestServer(&fakeStore{eventCounts: []storage.EventNameCount{
		{Name: "purchase", Count: 10}, {Name: "signup", Count: 4},
	}})
	resp := call(t, under, wildcardActor(), "tools/call", callParams{
		Name: "event_audit", Arguments: json.RawMessage(`{"site":"1","range":"30d"}`),
	})
	sc := mustCallResult(t, resp).StructuredContent.(map[string]any)
	if sc["cap_status"] != "ok" || sc["distinct"] != float64(2) {
		t.Errorf("under-cap: cap_status=%v distinct=%v, want ok/2", sc["cap_status"], sc["distinct"])
	}

	// 4 distinct → over the ceiling → "over".
	over := newTestServer(&fakeStore{eventCounts: []storage.EventNameCount{
		{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"},
	}})
	resp = call(t, over, wildcardActor(), "tools/call", callParams{
		Name: "event_audit", Arguments: json.RawMessage(`{"site":"1","range":"30d"}`),
	})
	sc = mustCallResult(t, resp).StructuredContent.(map[string]any)
	if sc["cap_status"] != "over" {
		t.Errorf("over-cap: cap_status=%v, want over", sc["cap_status"])
	}
}

// TestEventAudit_EventNamesSanitized — event names are operator/UGC strings; a
// crafted name must be neutralized before reaching the model.
func TestEventAudit_EventNamesSanitized(t *testing.T) {
	t.Parallel()

	s := newTestServer(&fakeStore{eventCounts: []storage.EventNameCount{
		{Name: "buy" + string(rune(0x200B)) + "<!--x-->", Count: 3},
	}})

	resp := call(t, s, wildcardActor(), "tools/call", callParams{
		Name: "event_audit", Arguments: json.RawMessage(`{"site":"1","range":"7d"}`),
	})

	sc := mustCallResult(t, resp).StructuredContent.(map[string]any)
	events := sc["events"].([]any)
	name := events[0].(map[string]any)["name"].(string)

	if name != "buy" {
		t.Errorf("event name not sanitized: %q", name)
	}
}

func TestEventAudit_NotAvailableWithoutConcrete(t *testing.T) {
	t.Parallel()

	// New() without Concrete → event_audit has no data source → isError.
	s := New(Config{
		Store:      &fakeStore{},
		Registry:   newTestRegistry(),
		Version:    "t",
		GeoEnabled: true,
		Budget:     BudgetConfig{CallsPerMin: 100, WildcardFactor: 1},
		Now:        func() time.Time { return testNow },
	})

	ct := mustCallResult(t, call(t, s, wildcardActor(), "tools/call", callParams{
		Name: "event_audit", Arguments: json.RawMessage(`{"site":"1","range":"7d"}`),
	}))
	if !ct.IsError {
		t.Error("event_audit without a concrete reader should return isError")
	}
}

func TestToolsCall_SiteConfig_NoPII(t *testing.T) {
	t.Parallel()

	resp := call(t, newTestServer(&fakeStore{}), wildcardActor(), "tools/call", callParams{
		Name: "site_config", Arguments: json.RawMessage(`{"site":"1"}`),
	})

	ct := mustCallResult(t, resp)
	if ct.IsError {
		t.Fatalf("site_config errored: %+v", ct)
	}

	sc := ct.StructuredContent.(map[string]any)

	for _, want := range []string{"site_id", "hostname", "consent_mode", "jurisdiction", "respect_gpc"} {
		if _, ok := sc[want]; !ok {
			t.Errorf("site_config missing %q", want)
		}
	}

	for _, banned := range []string{"email", "user_id", "password", "password_hash"} {
		if _, ok := sc[banned]; ok {
			t.Errorf("site_config leaked PII field %q", banned)
		}
	}
}

func TestToolsCall_MyAccess(t *testing.T) {
	t.Parallel()

	s := newTestServer(&fakeStore{})

	// Scoped actor → enumerated grants, not wildcard.
	scoped := mustCallResult(t, call(t, s, syntheticOperator([]uint32{1, 2}, false), "tools/call",
		callParams{Name: "my_access", Arguments: json.RawMessage(`{}`)}))
	sc := scoped.StructuredContent.(map[string]any)

	if sc["wildcard"] != false {
		t.Errorf("scoped actor wildcard = %v, want false", sc["wildcard"])
	}

	if grants, _ := sc["sites"].([]any); len(grants) != 2 {
		t.Errorf("scoped actor sites = %v, want 2", sc["sites"])
	}

	// No PII fields.
	for _, banned := range []string{"email", "user_id", "username"} {
		if _, ok := sc[banned]; ok {
			t.Errorf("my_access leaked %q", banned)
		}
	}

	// Wildcard actor → wildcard:true.
	wild := mustCallResult(t, call(t, s, wildcardActor(), "tools/call",
		callParams{Name: "my_access", Arguments: json.RawMessage(`{}`)}))
	if wild.StructuredContent.(map[string]any)["wildcard"] != true {
		t.Errorf("wildcard actor wildcard != true: %v", wild.StructuredContent)
	}

	// api-token actor (single site) → that one grant.
	tok := mustCallResult(t, call(t, s, apiTokenActor(7), "tools/call",
		callParams{Name: "my_access", Arguments: json.RawMessage(`{}`)}))
	tsc := tok.StructuredContent.(map[string]any)
	if tsc["role"] != "api" {
		t.Errorf("token actor role = %v, want api", tsc["role"])
	}

	if grants, _ := tsc["sites"].([]any); len(grants) != 1 {
		t.Errorf("token actor sites = %v, want 1", tsc["sites"])
	}
}

// TestMyAccess_NotSiteScoped confirms my_access needs no site arg and resolves
// nothing (global tool) — a bare {} call from a wildcard actor succeeds.
func TestMyAccess_NotSiteScoped(t *testing.T) {
	t.Parallel()

	resp := call(t, newTestServer(&fakeStore{}), wildcardActor(), "tools/call",
		callParams{Name: "my_access", Arguments: json.RawMessage(`{}`)})

	if mustCallResult(t, resp).IsError {
		t.Error("my_access with no site should succeed (global tool)")
	}
}

// TestAdminTools_AuditDenied confirms an api-role denial on an admin tool
// emits the audit event without leaking args.
func TestAdminTools_AuditDeniedNoArgsLeak(t *testing.T) {
	t.Parallel()

	// Reuse the role-floor denial path; the audit assertion is in audit_test
	// for tool_call. Here we just confirm the denial shape is stable: the
	// dispatcher rejects BEFORE building a filter, so no secret arg is read.
	const secret = "ADMIN-PROBE-SECRET"

	resp := call(t, newTestServer(&fakeStore{}), apiTokenActor(1), "tools/call", callParams{
		Name:      "event_audit",
		Arguments: json.RawMessage(`{"site":"1","filters":{"referrer":"` + secret + `"}}`),
	})

	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("want -32602, got %+v", resp.Error)
	}

	// The error message must not echo the supplied secret.
	if strings.Contains(resp.Error.Message, secret) {
		t.Errorf("error message leaked arg value: %q", resp.Error.Message)
	}
}
