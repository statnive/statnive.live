package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/auth"
)

func TestClampLimit(t *testing.T) {
	t.Parallel()

	cases := map[int]int{-5: 0, 0: 0, 50: 50, 500: 500, 501: 500, 1_000_000: 500}
	for in, want := range cases {
		if got := clampLimit(in); got != want {
			t.Errorf("clampLimit(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestBuildFilter_ClampsAndValidates(t *testing.T) {
	t.Parallel()

	f, err := buildFilter(1, toolArgs{Range: "7d", Limit: 1_000_000}, time.UTC, testNow)
	if err != nil {
		t.Fatalf("buildFilter: %v", err)
	}

	if f.Limit != 500 {
		t.Errorf("limit = %d, want 500 (clamped)", f.Limit)
	}

	if f.Offset != 0 {
		t.Errorf("offset = %d, want 0 (never set)", f.Offset)
	}

	if !f.From.Before(f.To) {
		t.Error("from must be before to")
	}
}

func TestBuildFilter_BadRangeRejected(t *testing.T) {
	t.Parallel()

	if _, err := buildFilter(1, toolArgs{Range: "9999d"}, time.UTC, testNow); err == nil {
		t.Error("9999d should be rejected (exceeds cap)")
	}
}

func TestDecodeStrict_RejectsUnknownField(t *testing.T) {
	t.Parallel()

	var a toolArgs
	if err := decodeStrict(json.RawMessage(`{"site":"1","bogus":true}`), &a); err == nil {
		t.Error("unknown top-level field should be rejected")
	}

	var b toolArgs
	if err := decodeStrict(json.RawMessage(`{"site":"1","filters":{"nope":"x"}}`), &b); err == nil {
		t.Error("unknown nested filter field should be rejected")
	}

	var c toolArgs
	if err := decodeStrict(json.RawMessage(`{"site":"1","filters":{"path":"/p"}}`), &c); err != nil {
		t.Errorf("valid args rejected: %v", err)
	}

	if c.Filters.Path != "/p" {
		t.Errorf("filters.path = %q, want /p", c.Filters.Path)
	}
}

func TestRoleFloor(t *testing.T) {
	t.Parallel()

	api := &auth.User{Role: auth.RoleAPI}
	viewer := &auth.User{Role: auth.RoleViewer}
	admin := &auth.User{Role: auth.RoleAdmin}

	// Analytics tools have a RoleAPI floor — every role meets it.
	for _, u := range []*auth.User{api, viewer, admin} {
		if !meetsRoleFloor(u, auth.RoleAPI) {
			t.Errorf("role %s should meet RoleAPI floor", u.Role)
		}
	}

	// Admin floor — only admin passes (the api-token escalation guard).
	if meetsRoleFloor(api, auth.RoleAdmin) || meetsRoleFloor(viewer, auth.RoleAdmin) {
		t.Error("api/viewer must NOT meet RoleAdmin floor")
	}

	if !meetsRoleFloor(admin, auth.RoleAdmin) {
		t.Error("admin must meet RoleAdmin floor")
	}

	if meetsRoleFloor(nil, auth.RoleAPI) {
		t.Error("nil actor must fail")
	}
}

func TestSyntheticOperator_Scoping(t *testing.T) {
	t.Parallel()

	// Fail-closed default: reads nothing.
	failClosed := syntheticOperator(nil, false)
	if failClosed.ActorCanReadSite(1) || isWildcardActor(failClosed) {
		t.Error("fail-closed operator must read no site and not be wildcard")
	}

	// Scoped: reads granted sites only.
	scoped := syntheticOperator([]uint32{1, 4}, false)
	if !scoped.ActorCanReadSite(1) || !scoped.ActorCanReadSite(4) {
		t.Error("scoped operator must read granted sites")
	}

	if scoped.ActorCanReadSite(2) || isWildcardActor(scoped) {
		t.Error("scoped operator must NOT read un-granted site 2 and not be wildcard")
	}

	// All-sites: wildcard.
	all := syntheticOperator(nil, true)
	if !all.ActorCanReadSite(99) || !isWildcardActor(all) {
		t.Error("--all-sites operator must read any site and be wildcard")
	}
}

func TestBudget_PerMinuteCallCap(t *testing.T) {
	t.Parallel()

	now := testNow
	b := newBudgetSet(BudgetConfig{CallsPerMin: 2, WildcardFactor: 0.25}, func() time.Time { return now })

	if ok, _ := b.reserve("k", false, 0); !ok {
		t.Fatal("call 1 should pass")
	}

	if ok, _ := b.reserve("k", false, 0); !ok {
		t.Fatal("call 2 should pass")
	}

	if ok, retry := b.reserve("k", false, 0); ok || retry < 1 {
		t.Fatalf("call 3 should be denied with retry≥1, got ok=%v retry=%d", ok, retry)
	}

	// Window reset after a minute.
	now = now.Add(61 * time.Second)

	if ok, _ := b.reserve("k", false, 0); !ok {
		t.Fatal("after window reset, call should pass")
	}
}

func TestBudget_WildcardStrictTier(t *testing.T) {
	t.Parallel()

	b := newBudgetSet(BudgetConfig{CallsPerMin: 4, WildcardFactor: 0.25}, func() time.Time { return testNow })

	// Wildcard tier = 4 * 0.25 = 1 call.
	if ok, _ := b.reserve("w", true, 0); !ok {
		t.Fatal("wildcard call 1 should pass")
	}

	if ok, _ := b.reserve("w", true, 0); ok {
		t.Fatal("wildcard call 2 should be denied (strict ×0.25 tier)")
	}
}

func TestMarshalResult_SanitizesAndShapes(t *testing.T) {
	t.Parallel()

	type row struct {
		Referrer string `json:"referrer"`
		Views    int    `json:"views"`
	}

	in := []row{{Referrer: "go" + string(rune(0x200B)) + "ogle<!--x-->", Views: 5}}

	structured, text, err := marshalResult(in)
	if err != nil {
		t.Fatalf("marshalResult: %v", err)
	}

	arr, ok := structured.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("structured shape wrong: %T", structured)
	}

	first, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("structured row not a map: %T", arr[0])
	}

	got, ok := first["referrer"].(string)
	if !ok {
		t.Fatalf("referrer not a string: %T", first["referrer"])
	}

	if got != "google" {
		t.Errorf("referrer not sanitized: %q", got)
	}

	if strings.Contains(text, "<!--") || strings.Contains(text, string(rune(0x200B))) {
		t.Errorf("text block not sanitized: %q", text)
	}
}

func TestToolSchemasAreValidJSON(t *testing.T) {
	t.Parallel()

	for _, td := range catalog() {
		if !json.Valid(td.InputSchema) {
			t.Errorf("%s: inputSchema is not valid JSON", td.Name)
		}

		if len(td.OutputSchema) > 0 && !json.Valid(td.OutputSchema) {
			t.Errorf("%s: outputSchema is not valid JSON", td.Name)
		}

		if td.Handler == nil {
			t.Errorf("%s: nil handler", td.Name)
		}

		if !td.Annotations.ReadOnlyHint {
			t.Errorf("%s: must be readOnly", td.Name)
		}
	}
}

func TestToolDescriptionsAreCleanASCII(t *testing.T) {
	t.Parallel()

	// We must never become a poisoning vector: descriptions/names carry no
	// invisible Unicode / HTML comments / instruction markers.
	for _, td := range catalog() {
		if !textsanClean(td.Name) {
			t.Errorf("tool name %q is not clean", td.Name)
		}

		if !textsanClean(td.Description) {
			t.Errorf("tool %q description is not clean", td.Name)
		}
	}
}

// textsanClean is a tiny indirection so the test reads clearly; it asserts
// the description survives the sanitizer unchanged.
func textsanClean(s string) bool {
	structured, _, err := marshalResult(map[string]string{"s": s})
	if err != nil {
		return false
	}

	m, ok := structured.(map[string]any)
	if !ok {
		return false
	}

	got, ok := m["s"].(string)
	if !ok {
		return false
	}

	return got == s
}
