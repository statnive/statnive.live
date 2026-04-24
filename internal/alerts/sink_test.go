package alerts_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/alerts"
)

func TestSink_EmitAndReopen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "alerts.jsonl")

	s, err := alerts.New(path, "test-host")
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	s.Emit(context.Background(), "wal_high_fill_ratio", alerts.SeverityWarn, false)

	lines := readJSONLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}

	if lines[0]["alert"] != "wal_high_fill_ratio" ||
		lines[0]["severity"] != "warn" ||
		lines[0]["resolved"] != false ||
		lines[0]["host"] != "test-host" {
		t.Errorf("unexpected payload: %#v", lines[0])
	}

	// Emulate logrotate: rename, reopen, emit — second event lands in
	// the new file.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	if err := s.Reopen(); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	s.Emit(context.Background(), "clickhouse_up", alerts.SeverityInfo, true)

	lines = readJSONLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("post-reopen: got %d lines, want 1", len(lines))
	}

	if lines[0]["alert"] != "clickhouse_up" || lines[0]["resolved"] != true {
		t.Errorf("post-reopen payload: %#v", lines[0])
	}
}

func TestSink_NilReceiverSafe(t *testing.T) {
	t.Parallel()

	var s *alerts.Sink
	// Nil sink must accept emit / reopen / close without panic — that's
	// the "alerts.sink_path unset" operator posture.
	s.Emit(context.Background(), "noop", alerts.SeverityInfo, false)

	if err := s.Reopen(); err != nil {
		t.Errorf("nil reopen: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Errorf("nil close: %v", err)
	}
}

func TestBandTracker_EnterExitTransitions(t *testing.T) {
	t.Parallel()

	var tr alerts.BandTracker

	tr0 := tr.Observe(0)
	if tr0.Entered || tr0.Exited {
		t.Errorf("0→0 should be no-op: %#v", tr0)
	}

	tr1 := tr.Observe(1)
	if !tr1.Entered || tr1.Band != 1 {
		t.Errorf("0→1 should enter band 1: %#v", tr1)
	}

	tr2 := tr.Observe(2)
	if !tr2.Entered || tr2.Band != 2 {
		t.Errorf("1→2 should enter band 2: %#v", tr2)
	}

	tr2b := tr.Observe(2)
	if tr2b.Entered || tr2b.Exited {
		t.Errorf("2→2 should be no-op: %#v", tr2b)
	}

	tr0b := tr.Observe(0)
	if !tr0b.Exited || tr0b.Band != 0 {
		t.Errorf("2→0 should exit: %#v", tr0b)
	}
}

func readJSONLines(t *testing.T, path string) []map[string]any {
	t.Helper()

	f, err := os.Open(path) //nolint:gosec // test helper; path is test-controlled
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}

	defer func() { _ = f.Close() }()

	var out []map[string]any

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}

		out = append(out, m)
	}

	return out
}
