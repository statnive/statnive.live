package httpapi_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"

	"github.com/statnive/statnive.live/internal/specgen"
)

// nonChiRoutes are HTTP surfaces NOT on the main chi router, so chi.Walk can't
// see them — they are hand-authored in the overlay and pinned here so the
// contract can't silently drop them. (OAuth-AS is a chatgpt_app-build stub in
// the default build; MCP-over-HTTP is a separate ServeMux subcommand.)
var nonChiRoutes = []struct{ method, path string }{
	{"GET", "/.well-known/oauth-authorization-server"},
	{"GET", "/.well-known/jwks.json"},
	{"POST", "/token"},
	{"GET", "/authorize"},
	{"POST", "/consent"},
	{"POST", "/register"},
	{"POST", "/mcp"},
	{"GET", "/.well-known/oauth-protected-resource"},
}

// specPath resolves api/openapi.yaml relative to this test file (repo-root/api).
func specPath() string {
	_, thisFile, _, _ := runtime.Caller(0) //nolint:dogsled // only the caller's file path is needed
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "api", "openapi.yaml")
}

type openapiDoc struct {
	Paths map[string]map[string]any `yaml:"paths"`
}

// loadSpec parses the committed contract, or skips the test when it doesn't
// exist yet (pre-Phase-C). Once api/openapi.yaml is committed these become hard
// gates.
func loadSpec(t *testing.T) openapiDoc {
	t.Helper()

	b, err := os.ReadFile(specPath())
	if err != nil {
		t.Skipf("api/openapi.yaml not present yet (%v) — generated/merged in Phase B/C", err)
	}

	var doc openapiDoc
	if uErr := yaml.Unmarshal(b, &doc); uErr != nil {
		t.Fatalf("parse openapi.yaml: %v", uErr)
	}

	if len(doc.Paths) == 0 {
		t.Fatal("openapi.yaml has no paths")
	}

	return doc
}

func TestSpec_EveryRouteDocumented(t *testing.T) {
	t.Parallel()
	doc := loadSpec(t)

	routes, err := specgen.Routes()
	if err != nil {
		t.Fatalf("specgen.Routes: %v", err)
	}

	for _, r := range routes {
		item, ok := doc.Paths[r.Path]
		if !ok {
			t.Errorf("route %s %s registered but path missing from openapi.yaml", r.Method, r.Path)
			continue
		}

		if _, ok := item[strings.ToLower(r.Method)]; !ok {
			t.Errorf("route %s %s registered but method undocumented in openapi.yaml", r.Method, r.Path)
		}
	}
}

func TestSpec_NoOrphanSpecPaths(t *testing.T) {
	t.Parallel()
	doc := loadSpec(t)

	routes, err := specgen.Routes()
	if err != nil {
		t.Fatalf("specgen.Routes: %v", err)
	}

	live := map[string]bool{}
	for _, r := range routes {
		live[strings.ToLower(r.Method)+" "+r.Path] = true
	}
	// nonChiRoutes are legitimately in the spec but not on the chi router.
	nonChi := map[string]bool{}
	for _, n := range nonChiRoutes {
		nonChi[strings.ToLower(n.method)+" "+n.path] = true
	}

	httpMethods := map[string]bool{
		"get": true, "post": true, "put": true, "delete": true,
		"patch": true, "options": true, "head": true, "trace": true,
	}

	for path, item := range doc.Paths {
		for method := range item {
			if !httpMethods[strings.ToLower(method)] {
				continue // parameters, summary, $ref, etc.
			}

			key := strings.ToLower(method) + " " + path
			if !live[key] && !nonChi[key] {
				t.Errorf("openapi.yaml documents %s %s but no such route exists (orphan)", strings.ToUpper(method), path)
			}
		}
	}
}

func TestSpec_NonChiSurfacesDocumented(t *testing.T) {
	t.Parallel()
	doc := loadSpec(t)

	for _, n := range nonChiRoutes {
		item, ok := doc.Paths[n.path]
		if !ok {
			t.Errorf("non-chi surface %s %s missing from openapi.yaml (hand-authored overlay must include it)", n.method, n.path)
			continue
		}

		if _, ok := item[strings.ToLower(n.method)]; !ok {
			t.Errorf("non-chi surface %s %s present but method undocumented", n.method, n.path)
		}
	}
}

// TestSpec_SkeletonDeterministic guards the drift gate: emitting the skeleton
// twice must be byte-identical (no map iteration, no timestamps). Runs without
// any committed artifact.
func TestSpec_SkeletonDeterministic(t *testing.T) {
	t.Parallel()

	routes, err := specgen.Routes()
	if err != nil {
		t.Fatalf("specgen.Routes: %v", err)
	}

	a := specgen.Skeleton(routes)

	b := specgen.Skeleton(routes)
	if string(a) != string(b) {
		t.Fatal("skeleton emission is non-deterministic")
	}

	if len(routes) < 60 {
		t.Fatalf("expected >= 60 documented routes, got %d", len(routes))
	}
}
