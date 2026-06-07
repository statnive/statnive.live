// Command specgen derives the OpenAPI contract from the live chi router.
//
// It walks the router (internal/specgen) to emit a deterministic skeleton
// (api/openapi.gen.yaml: paths + methods + operationId + tag, no responses),
// then deep-merges the hand-authored api/overlay.yaml over it to produce the
// committed contract api/openapi.yaml. Routes therefore can never drift from
// the code; semantics stay hand-authored in the overlay.
//
// Run from the repo root (the Makefile `spec-build` target does):
//
//	go run ./cmd/specgen
//
// Both outputs are committed; `spec-check` re-runs this and `git diff`s api/.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/statnive/statnive.live/internal/specgen"
)

func main() {
	dir := flag.String("dir", "api", "output directory for the contract artifacts")
	flag.Parse()

	if err := run(*dir); err != nil {
		fmt.Fprintf(os.Stderr, "specgen: %v\n", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	routes, err := specgen.Routes()
	if err != nil {
		return fmt.Errorf("walk routes: %w", err)
	}

	if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
		return fmt.Errorf("mkdir %s: %w", dir, mkErr)
	}

	skeleton := specgen.Skeleton(routes)
	genPath := filepath.Join(dir, "openapi.gen.yaml")

	if wErr := os.WriteFile(genPath, skeleton, 0o600); wErr != nil {
		return fmt.Errorf("write %s: %w", genPath, wErr)
	}

	overlayPath := filepath.Join(dir, "overlay.yaml")

	overlay, oErr := os.ReadFile(overlayPath) //nolint:gosec // G304: repo-relative contract file, not user input
	if oErr != nil {
		if os.IsNotExist(oErr) {
			fmt.Fprintf(os.Stderr, "specgen: wrote %s (%d operations); no overlay yet — skipping merge\n", genPath, len(routes))

			return nil
		}

		return fmt.Errorf("read %s: %w", overlayPath, oErr)
	}

	merged, mErr := specgen.Merge(skeleton, overlay)
	if mErr != nil {
		return fmt.Errorf("merge overlay: %w", mErr)
	}

	outPath := filepath.Join(dir, "openapi.yaml")

	if wErr := os.WriteFile(outPath, merged, 0o600); wErr != nil {
		return fmt.Errorf("write %s: %w", outPath, wErr)
	}

	fmt.Fprintf(os.Stderr, "specgen: wrote %s + %s (%d operations)\n", genPath, outPath, len(routes))

	return nil
}
