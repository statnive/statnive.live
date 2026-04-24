package audit_test

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/statnive/statnive.live/internal/audit"
)

func TestLogger_AppendsJSONL(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "audit.jsonl")

	l, err := audit.New(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() { _ = l.Close() })

	for i := range 5 {
		l.Event(context.Background(), audit.EventTLSCertLoaded,
			slog.Int("seq", i),
		)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	lines := readLines(t, path)
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5", len(lines))
	}

	for i, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Errorf("line %d: not valid JSON: %v", i, err)

			continue
		}

		if record["event"] != string(audit.EventTLSCertLoaded) {
			t.Errorf("line %d: event = %v, want %s", i, record["event"], audit.EventTLSCertLoaded)
		}
	}
}

func TestLogger_ReopenAfterRotation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	l, err := audit.New(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() { _ = l.Close() })

	l.Event(context.Background(), audit.EventTLSCertLoaded, slog.String("phase", "before-rotate"))

	rotated := filepath.Join(dir, "audit.jsonl.1")
	if err := os.Rename(path, rotated); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	if err := l.Reopen(); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	l.Event(context.Background(), audit.EventTLSCertLoaded, slog.String("phase", "after-rotate"))

	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	beforeLines := readLines(t, rotated)
	if len(beforeLines) != 1 || !strings.Contains(beforeLines[0], "before-rotate") {
		t.Errorf("rotated file should hold 1 event with before-rotate; got %v", beforeLines)
	}

	afterLines := readLines(t, path)
	// Expect 2 lines in the new file: the after-rotate event + the
	// reopen-succeeded marker the audit logger writes itself.
	if len(afterLines) != 2 {
		t.Fatalf("new file should hold 2 events (reopen + after-rotate); got %d: %v", len(afterLines), afterLines)
	}
}

func TestLogger_ConcurrentWritesNeverInterleave(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "audit.jsonl")

	l, err := audit.New(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() { _ = l.Close() })

	const (
		workers         = 50
		eventsPerWorker = 100
	)

	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)

		go func(workerID int) {
			defer wg.Done()

			for i := range eventsPerWorker {
				l.Event(context.Background(), audit.EventFastReject,
					slog.Int("worker", workerID),
					slog.Int("seq", i),
				)
			}
		}(w)
	}

	wg.Wait()

	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	lines := readLines(t, path)
	want := workers * eventsPerWorker

	if len(lines) != want {
		t.Fatalf("got %d lines, want %d (events lost or interleaved)", len(lines), want)
	}

	for i, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %d not valid JSON: %v\nline=%q", i, err, line)
		}
	}
}

func TestLogger_EmptyPathRejected(t *testing.T) {
	t.Parallel()

	if _, err := audit.New(""); err == nil {
		t.Error("expected error for empty path")
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}

	defer func() { _ = f.Close() }()

	var lines []string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}

	return lines
}
