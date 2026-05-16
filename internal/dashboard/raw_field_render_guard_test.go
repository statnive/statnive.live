// Package dashboard regression guard — pins Architecture Rule 1
// ("Raw table is WRITE-ONLY") at the dashboard read layer.
//
// The /api/event ingest path stores user-controllable strings (title,
// event_name, props, user_segment, raw referrer, ...) into events_raw.
// Today the dashboard only reads pre-aggregated rollups, so those raw
// strings never reach an HTTP response and are not an XSS sink. This
// test fails any future PR that:
//
//   - imports `text/template` into the dashboard package (text/template
//     does not auto-escape — only html/template does), or
//   - selects one of the raw user-controllable fields (.Title, .EventName,
//     .Props, .PropKeys, .PropVals, .UserSegment) in a non-test file in
//     this package, or
//   - adds a json tag of "title", "event_name", "props", "prop_keys",
//     "prop_vals", or "user_segment" to any of the storage result rows
//     consumed by the dashboard handlers.
//
// If you legitimately need to surface one of these fields, do all three:
//
//  1. Update this test with a documented exception block.
//  2. Escape the value at the boundary with html.EscapeString (or use
//     html/template, never text/template).
//  3. Return JSON only (Content-Type: application/json), and never
//     concatenate the value into an HTML string anywhere.
//
// Audit and plan: ingest XSS-hardening (commit message + LEARN.md entry
// once the gate ships) — every error message below names this file so
// `grep raw_field_render_guard_test.go` from the PR turns up the rule.
package dashboard_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// forbiddenSelectors are field names that, if accessed in this package,
// signal a raw user-controllable string is being read out. Name-based
// matching is sufficient because none of these identifiers appear in
// internal/dashboard/ today — any future appearance is a finding to
// review, even if it turns out to be unrelated.
var forbiddenSelectors = map[string]struct{}{
	"Title":       {}, // page <title> from tracker
	"EventName":   {}, // custom event name
	"Props":       {}, // custom event property map
	"PropKeys":    {}, // CH Array(String) columns
	"PropVals":    {},
	"UserSegment": {}, // tracker-supplied segment label
}

// forbiddenJSONTags are the corresponding JSON field tags. If a dashboard
// result row (PageRow / SourceRow / CampaignRow / ...) ever grows one of
// these tags, the wire format has surfaced a raw user-controllable
// string and this guard fails.
var forbiddenJSONTags = map[string]struct{}{
	"title":        {},
	"event_name":   {},
	"props":        {},
	"prop_keys":    {},
	"prop_vals":    {},
	"user_segment": {},
}

func TestDashboardPackage_NoTextTemplateImport(t *testing.T) {
	t.Parallel()

	files := dashboardSourceFiles(t)

	fset := token.NewFileSet()

	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		for _, imp := range f.Imports {
			if imp.Path.Value == `"text/template"` {
				t.Errorf("%s imports text/template — use html/template (auto-escapes) "+
					"or encoding/json only (see raw_field_render_guard_test.go)", path)
			}
		}
	}
}

func TestDashboardPackage_NoRawFieldSelectors(t *testing.T) {
	t.Parallel()

	files := dashboardSourceFiles(t)

	fset := token.NewFileSet()

	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		// Build the set of imported package short names. A selector
		// like `audit.EventName` is a type qualifier from an imported
		// package, not a field access on a raw-event value — skip
		// those. Field accesses (`row.Title`) have an LHS that is a
		// local identifier, not an imported package name.
		importNames := packageImportNames(f)

		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			if _, bad := forbiddenSelectors[sel.Sel.Name]; !bad {
				return true
			}

			if x, ok := sel.X.(*ast.Ident); ok {
				if _, isImport := importNames[x.Name]; isImport {
					return true
				}
			}

			pos := fset.Position(sel.Pos())
			t.Errorf("%s:%d: selector .%s — raw user-controllable field; if you need to render it, "+
				"escape at the boundary and update this guard with an exception block",
				pos.Filename, pos.Line, sel.Sel.Name)

			return true
		})
	}
}

// packageImportNames returns the set of short names under which f
// imports other packages. For `import audit "github.com/.../audit"` or
// `import "github.com/.../audit"` the short name is "audit". Used to
// distinguish type qualifiers (audit.EventName) from raw-event field
// accesses (row.EventName).
func packageImportNames(f *ast.File) map[string]struct{} {
	out := make(map[string]struct{}, len(f.Imports))

	for _, imp := range f.Imports {
		var name string

		if imp.Name != nil {
			name = imp.Name.Name
		} else {
			path := strings.Trim(imp.Path.Value, `"`)
			if slash := strings.LastIndexByte(path, '/'); slash >= 0 {
				name = path[slash+1:]
			} else {
				name = path
			}
		}

		if name == "" || name == "_" || name == "." {
			continue
		}

		out[name] = struct{}{}
	}

	return out
}

func TestStorageResultRows_NoRawFieldJSONTags(t *testing.T) {
	t.Parallel()

	// Walk every result row struct exposed by internal/storage to the
	// dashboard. Fail if any field carries a json tag that would
	// surface a raw user-controllable string on the wire.
	resultPath := dashboardSiblingFile(t, "../storage/result.go")

	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, resultPath, nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse %s: %v", resultPath, err)
	}

	ast.Inspect(f, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok {
			return true
		}

		for _, field := range st.Fields.List {
			if field.Tag == nil {
				continue
			}

			tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))

			jsonTag, _, _ := strings.Cut(tag.Get("json"), ",")

			if _, bad := forbiddenJSONTags[jsonTag]; bad {
				pos := fset.Position(field.Pos())
				t.Errorf("%s:%d: struct field with json:%q — raw user-controllable wire field "+
					"(see raw_field_render_guard_test.go)", pos.Filename, pos.Line, jsonTag)
			}
		}

		return true
	})
}

func dashboardSourceFiles(t *testing.T) []string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatalf("read dashboard dir: %v", err)
	}

	out := make([]string, 0, len(entries))

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		out = append(out, filepath.Join(wd, name))
	}

	if len(out) == 0 {
		t.Fatal("no .go source files found in dashboard package")
	}

	return out
}

func dashboardSiblingFile(t *testing.T, rel string) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	abs, err := filepath.Abs(filepath.Join(wd, rel))
	if err != nil {
		t.Fatalf("abs %s: %v", rel, err)
	}

	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("stat %s: %v", abs, err)
	}

	return abs
}
