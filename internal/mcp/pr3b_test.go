package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/storage"
)

func TestToolsCall_About(t *testing.T) {
	t.Parallel()

	resp := call(t, newTestServer(&fakeStore{}), wildcardActor(), "tools/call",
		callParams{Name: "about", Arguments: json.RawMessage(`{}`)})

	ct := mustCallResult(t, resp)
	if ct.IsError {
		t.Fatalf("about errored: %+v", ct)
	}

	sc := ct.StructuredContent.(map[string]any)

	attrs, _ := sc["attributions"].([]any)
	if len(attrs) == 0 {
		t.Fatal("about must include third-party attributions (IP2Location LITE)")
	}

	// The CC-BY-SA IP2Location LITE verbatim notice must be present somewhere.
	const verbatim = "This site or product includes IP2Location LITE data available from https://lite.ip2location.com."

	found := false

	for _, a := range attrs {
		if a.(map[string]any)["text"] == verbatim {
			found = true
		}
	}

	if !found {
		t.Errorf("about missing the verbatim IP2Location LITE attribution: %v", attrs)
	}

	// No PII / secret keys.
	for _, banned := range []string{"email", "user_id", "master_secret", "password"} {
		if _, ok := sc[banned]; ok {
			t.Errorf("about leaked %q", banned)
		}
	}
}

// TestAbout_ReachableByAnyRole — about is public-ish (RoleAPI); an api-token
// actor can call it.
func TestAbout_ReachableByAnyRole(t *testing.T) {
	t.Parallel()

	resp := call(t, newTestServer(&fakeStore{}), apiTokenActor(1), "tools/call",
		callParams{Name: "about", Arguments: json.RawMessage(`{}`)})

	if mustCallResult(t, resp).IsError {
		t.Error("about should be reachable by an api-role actor")
	}
}

func TestSystemHealth_AdminOnly(t *testing.T) {
	t.Parallel()

	s := newTestServer(&fakeStore{})

	// api-role denied with -32602 (matches REST 403 on the admin surface).
	resp := call(t, s, apiTokenActor(1), "tools/call",
		callParams{Name: "system_health", Arguments: json.RawMessage(`{}`)})
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("system_health for api role should be -32602, got %+v", resp.Error)
	}

	// admin (wildcard) allowed → CH reported up (fakePinger returns nil).
	ct := mustCallResult(t, call(t, s, wildcardActor(), "tools/call",
		callParams{Name: "system_health", Arguments: json.RawMessage(`{}`)}))
	if ct.IsError {
		t.Fatalf("system_health (admin) errored: %+v", ct)
	}

	sc := ct.StructuredContent.(map[string]any)
	if sc["clickhouse"] != "up" {
		t.Errorf("clickhouse = %v, want up", sc["clickhouse"])
	}

	for _, banned := range []string{"email", "user_id", "wal_path", "master_secret"} {
		if _, ok := sc[banned]; ok {
			t.Errorf("system_health leaked %q", banned)
		}
	}
}

func TestSystemHealth_ReportsCHDown(t *testing.T) {
	t.Parallel()

	s := New(Config{
		Store:      &fakeStore{},
		Registry:   newTestRegistry(),
		Health:     fakePinger{err: storage.ErrNotImplemented}, // any non-nil error => "down"
		Version:    "t",
		GeoEnabled: true,
		Budget:     BudgetConfig{CallsPerMin: 100, WildcardFactor: 1},
		Now:        func() time.Time { return testNow },
	})

	ct := mustCallResult(t, call(t, s, wildcardActor(), "tools/call",
		callParams{Name: "system_health", Arguments: json.RawMessage(`{}`)}))

	if ct.StructuredContent.(map[string]any)["clickhouse"] != "down" {
		t.Errorf("clickhouse should be down on ping error: %v", ct.StructuredContent)
	}
}

func TestSystemHealth_UnknownWithoutPinger(t *testing.T) {
	t.Parallel()

	s := New(Config{
		Store:      &fakeStore{},
		Registry:   newTestRegistry(),
		Version:    "t",
		GeoEnabled: true,
		Budget:     BudgetConfig{CallsPerMin: 100, WildcardFactor: 1},
		Now:        func() time.Time { return testNow },
	})

	ct := mustCallResult(t, call(t, s, wildcardActor(), "tools/call",
		callParams{Name: "system_health", Arguments: json.RawMessage(`{}`)}))

	if ct.StructuredContent.(map[string]any)["clickhouse"] != "unknown" {
		t.Errorf("clickhouse should be unknown without a pinger: %v", ct.StructuredContent)
	}
}

// TestPR3bTools_Posture: about is RoleAPI (any authed); system_health is admin.
// Both are global (not site-scoped) and read-only.
func TestPR3bTools_Posture(t *testing.T) {
	t.Parallel()

	byName := map[string]toolDef{}
	for _, td := range catalog() {
		byName[td.Name] = td
	}

	if td := byName["about"]; td.RoleClass != "api" || td.Scoped || !td.Annotations.ReadOnlyHint {
		t.Errorf("about posture wrong: role=%q scoped=%v ro=%v", td.RoleClass, td.Scoped, td.Annotations.ReadOnlyHint)
	}

	if td := byName["system_health"]; td.RoleClass != "admin" || td.Scoped || !td.Annotations.ReadOnlyHint {
		t.Errorf("system_health posture wrong: role=%q scoped=%v ro=%v", td.RoleClass, td.Scoped, td.Annotations.ReadOnlyHint)
	}
}

// TestSystemHealth_BudgetApplies — even the global admin tools consume the
// per-actor budget (rate-limit/abuse).
func TestSystemHealth_BudgetApplies(t *testing.T) {
	t.Parallel()

	s := New(Config{
		Store:      &fakeStore{},
		Registry:   newTestRegistry(),
		Health:     fakePinger{},
		Version:    "t",
		GeoEnabled: true,
		Budget:     BudgetConfig{CallsPerMin: 1, WildcardFactor: 1},
		Now:        func() time.Time { return testNow },
	})

	args := callParams{Name: "system_health", Arguments: json.RawMessage(`{}`)}

	_ = mustCallResult(t, call(t, s, wildcardActor(), "tools/call", args))

	second := mustCallResult(t, call(t, s, wildcardActor(), "tools/call", args))
	if !second.IsError || !strings.Contains(second.Content[0].Text, "budget") {
		t.Errorf("2nd system_health call should hit the budget: %+v", second)
	}
}
