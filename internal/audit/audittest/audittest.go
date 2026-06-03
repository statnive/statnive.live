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
	"time"
)

// waitForEventInterval is the poll cadence used by WaitForEvent. 50ms
// keeps test wall-clock latency low while staying well above the
// stat() syscall cost on the audit JSONL file.
const waitForEventInterval = 50 * time.Millisecond

// ReadEventNames extracts the "event" field from each JSONL line in path.
// Empty lines are skipped. Lines without an "event" key are skipped (slog
// metadata lines, etc.).
func ReadEventNames(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // test helper — path is test-controlled
	if err != nil {
		t.Fatalf("audittest read %s: %v", path, err)
	}

	return parseEventNames(data)
}

// WaitForEvent polls the JSONL audit file at path and returns true
// when an event named eventName appears, or false on timeout. Used by
// integration tests that exercise async paths (e.g. the dsar_erase_
// completed event emitted by the spawnCompletionWatcher goroutine
// after the CH mutation lands). A missing file is treated as "no
// events yet" rather than a failure, so callers can start polling
// before the file is created.
func WaitForEvent(t *testing.T, path, eventName string, timeout time.Duration) bool {
	t.Helper()

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(waitForEventInterval)
	defer ticker.Stop()

	for {
		if containsEvent(path, eventName) {
			return true
		}

		if !time.Now().Before(deadline) {
			return false
		}

		<-ticker.C
	}
}

// containsEvent is the per-tick check for WaitForEvent. Single-pass
// substring scan with early-return — avoids the intermediate []string
// allocation parseEventNames does, which matters when WaitForEvent
// runs at 50ms cadence over a 15 s budget.
func containsEvent(path, eventName string) bool {
	data, err := os.ReadFile(path) //nolint:gosec // test helper — path is test-controlled
	if err != nil {
		return false
	}

	const key = `"event":"`

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

		if rest[:end] == eventName {
			return true
		}
	}

	return false
}

// parseEventNames walks JSONL bytes and returns the value of every
// "event":"..." key. Shared between ReadEventNames and WaitForEvent
// so the substring-parse rules stay in one place.
func parseEventNames(data []byte) []string {
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
