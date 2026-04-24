// Package audittest holds test-only helpers for reading audit JSONL
// files. Production code never imports this package.
//
// Why substring search instead of json.Unmarshal: integration tests read
// audit logs containing 100+ events; unmarshaling each line is wasted
// allocation when callers only need the "event" key. The format is
// stable (slog.NewJSONHandler emits keys in insertion order) so the
// substring scan is robust enough.
package audittest

import (
	"os"
	"strings"
	"testing"
)

// ReadEventNames extracts the "event" field from each JSONL line in path.
// Empty lines are skipped. Lines without an "event" key are skipped (slog
// metadata lines, etc.).
func ReadEventNames(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // test helper — path is test-controlled
	if err != nil {
		t.Fatalf("audittest read %s: %v", path, err)
	}

	const key = `"event":"`

	var out []string

	for _, line := range strings.Split(string(data), "\n") {
		idx := strings.Index(line, key)
		if idx < 0 {
			continue
		}

		rest := line[idx+len(key):]

		end := strings.IndexByte(rest, '"')
		if end < 0 {
			continue
		}

		out = append(out, rest[:end])
	}

	return out
}
