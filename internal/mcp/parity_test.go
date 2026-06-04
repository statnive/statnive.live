package mcp

import (
	"reflect"
	"testing"

	"github.com/statnive/statnive.live/internal/storage"
)

// storeMethodCoverage maps every read method on the storage.Store interface to
// the MCP tool that exposes it (or a documented exclusion). This is the no-gap
// parity gate: TestParity_EveryStoreMethodHasTool reflects over the interface
// and fails when a method has no entry — so adding a Store read method without
// an MCP tool (or an explicit, reasoned exclusion) breaks the build.
//
// When you add a read method to storage.Store, you MUST add it here AND ship
// the tool (or justify the exclusion). Value is either a catalog tool name or
// "exclude: <reason>".
var storeMethodCoverage = map[string]string{
	"Overview":         "overview",
	"Trend":            "trend",
	"Sources":          "sources",
	"SourcesByChannel": "sources", // folded into the sources tool
	"Pages":            "pages",
	"SEO":              "seo",
	"Campaigns":        "campaigns",
	"Realtime":         "realtime",
	"Geo":              "geo",
	"GeoTopCountries":  "geo", // folded into the geo tool
	"Devices":          "devices",
	"Funnel":           "funnel",
	"PropNames":        "props_list",
	"Compare":          "compare",
}

// offInterfaceCoverage maps concrete-type read methods (NOT on the Store
// interface) to their tool. Reflection over the interface can't see these, so
// they're pinned explicitly — the lesson from event_audit.
var offInterfaceCoverage = map[string]string{
	"EventNameCardinality": "event_audit",
}

func catalogToolNames() map[string]bool {
	names := make(map[string]bool)
	for _, td := range catalog() {
		names[td.Name] = true
	}

	return names
}

func TestParity_EveryStoreMethodHasTool(t *testing.T) {
	t.Parallel()

	tools := catalogToolNames()
	storeType := reflect.TypeOf((*storage.Store)(nil)).Elem()

	for i := range storeType.NumMethod() {
		method := storeType.Method(i).Name

		cover, ok := storeMethodCoverage[method]
		if !ok {
			t.Errorf("storage.Store.%s has no MCP coverage — add a tool + a storeMethodCoverage entry, or a documented exclusion", method)

			continue
		}

		if len(cover) >= 8 && cover[:8] == "exclude:" {
			continue
		}

		if !tools[cover] {
			t.Errorf("storage.Store.%s maps to tool %q which is not in the catalog", method, cover)
		}
	}
}

func TestParity_OffInterfaceReadsCovered(t *testing.T) {
	t.Parallel()

	tools := catalogToolNames()

	for method, tool := range offInterfaceCoverage {
		if !tools[tool] {
			t.Errorf("off-interface read %s maps to tool %q which is not in the catalog", method, tool)
		}
	}
}

// TestParity_NoStaleCoverageEntries catches the reverse drift: a coverage
// entry whose Store method no longer exists (method renamed/removed).
func TestParity_NoStaleCoverageEntries(t *testing.T) {
	t.Parallel()

	storeType := reflect.TypeOf((*storage.Store)(nil)).Elem()
	exists := make(map[string]bool, storeType.NumMethod())

	for i := range storeType.NumMethod() {
		exists[storeType.Method(i).Name] = true
	}

	for method := range storeMethodCoverage {
		if !exists[method] {
			t.Errorf("storeMethodCoverage has stale entry %q — no such storage.Store method", method)
		}
	}
}

// TestParity_CatalogIntegrity pins the catalog itself: unique names, non-nil
// handlers, valid annotations. Complements the schema-validity test.
func TestParity_CatalogIntegrity(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)

	for _, td := range catalog() {
		if td.Name == "" {
			t.Error("catalog has a tool with an empty name")
		}

		if seen[td.Name] {
			t.Errorf("duplicate tool name in catalog: %q", td.Name)
		}

		seen[td.Name] = true

		if td.Handler == nil {
			t.Errorf("%s: nil handler", td.Name)
		}

		if !td.Annotations.ReadOnlyHint {
			t.Errorf("%s: every MCP tool must be read-only", td.Name)
		}
	}
}
