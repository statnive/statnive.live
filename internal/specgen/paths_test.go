package specgen_test

import (
	"path/filepath"
	"runtime"
)

// repoRoot resolves the repository root relative to this test file
// (internal/specgen → ../..). Shared by the specgen_test path helpers so the
// runtime.Caller dance lives in one place.
func repoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0) //nolint:dogsled
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func apiDir() string   { return filepath.Join(repoRoot(), "api") }
func specPath() string { return filepath.Join(apiDir(), "openapi.yaml") }
