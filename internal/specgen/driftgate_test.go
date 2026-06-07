package specgen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/statnive/statnive.live/internal/specgen"
)

// TestContractInSync is the Go-only drift gate that rides inside `make test`
// (no redocly/Node): it regenerates the skeleton from the live router, merges
// the committed overlay, and asserts the result byte-equals the committed
// api/openapi.yaml. A route change or an overlay edit that wasn't followed by
// `make spec-build` fails here — the "go document me" signal without needing
// the contract toolchain installed.
func TestContractInSync(t *testing.T) {
	dir := apiDir()
	overlay, err := os.ReadFile(filepath.Join(dir, "overlay.yaml"))
	if err != nil {
		t.Skipf("api/overlay.yaml not present (%v)", err)
	}
	committed, err := os.ReadFile(filepath.Join(dir, "openapi.yaml"))
	if err != nil {
		t.Skipf("api/openapi.yaml not present (%v)", err)
	}

	routes, err := specgen.Routes()
	if err != nil {
		t.Fatalf("specgen.Routes: %v", err)
	}
	merged, err := specgen.Merge(specgen.Skeleton(routes), overlay)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	if string(merged) != string(committed) {
		t.Errorf("api/openapi.yaml is stale — run `make spec-build` and commit.\n" +
			"(router or overlay changed without regenerating the merged contract)")
	}

	// Also guard the committed skeleton.
	gen, err := os.ReadFile(filepath.Join(dir, "openapi.gen.yaml"))
	if err == nil && string(gen) != string(specgen.Skeleton(routes)) {
		t.Errorf("api/openapi.gen.yaml is stale — run `make spec-build` and commit.")
	}
}
