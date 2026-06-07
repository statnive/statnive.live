package specgen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSchemathesis_ConfigScope guards the fuzz blast radius: the Makefile
// spec-fuzz recipe must exclude every mutating/ingest/privacy/admin surface so
// a fuzz run can never write or export customer-shaped data, must be
// loopback-pinned, and must drop unsafe HTTP methods. A future widening of the
// scope fails here.
func TestSchemathesis_ConfigScope(t *testing.T) {
	t.Parallel()
	mkPath := filepath.Join(repoRoot(), "Makefile")
	b, err := os.ReadFile(mkPath)
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	mk := string(b)

	// Isolate the spec-fuzz recipe block (the target line at column 0, not the
	// "## spec-fuzz:" doc comment), up to the next target's doc comment.
	start := strings.Index(mk, "\nspec-fuzz:\n")
	if start < 0 {
		t.Fatal("spec-fuzz target not found in Makefile")
	}
	block := mk[start:]
	if end := strings.Index(block[len("\nspec-fuzz:\n"):], "\n## "); end >= 0 {
		block = block[:end+len("\nspec-fuzz:\n")]
	}

	mustContain := []string{
		"REFUSING non-loopback",                // fail-closed on non-loopback target
		"--exclude-path-regex '/api/privacy/'", // never fuzz DSAR (incl. GET access export)
		"--exclude-path-regex '/api/admin/'",   // never fuzz admin mutations
		"--exclude-path-regex '/legal/'",       // audit-emitting GETs
		"--exclude-method POST",                // no writes
		"--exclude-method DELETE",              // no deletes
	}
	for _, want := range mustContain {
		if !strings.Contains(block, want) {
			t.Errorf("spec-fuzz recipe missing safety guard: %q", want)
		}
	}

	// The ingest writer must never be in the include set.
	if strings.Contains(block, "/api/event") {
		t.Error("spec-fuzz must not reference /api/event (it writes to the WAL)")
	}
}
