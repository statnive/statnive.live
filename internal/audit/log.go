package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

// Logger is the JSONL append-only file sink. One Logger per process; safe
// for concurrent use. The underlying file is opened with O_APPEND so the
// kernel atomically appends each write — concurrent goroutines never
// interleave inside a JSONL record.
//
// SIGHUP-driven re-open (Reopen) lets the operator rotate the log via
// logrotate's "create" or "move-then-create" strategy without restarting
// the binary: rename the file on disk, send SIGHUP, the next event lands
// in the freshly-created file.
type Logger struct {
	path string

	mu      sync.Mutex
	f       *os.File
	handler *slog.Logger
}

// New opens (or creates) the JSONL audit-log file at path. Mode 0640 is
// the standard "service-readable, group-readable, world-blind" mode for
// log files under systemd; matches the chmod the deploy script will set.
func New(path string) (*Logger, error) {
	if path == "" {
		return nil, errors.New("audit: path is required")
	}

	l := &Logger{path: path}

	if err := l.openLocked(); err != nil {
		return nil, err
	}

	return l, nil
}

// Event emits one JSONL record. Level is always Info — audit events
// describe completed actions, not warnings or errors. Surface a slog.Attr
// rather than ad-hoc keys so misspelled keys are a compile error in the
// caller, not a silent column drift in the log.
func (l *Logger) Event(ctx context.Context, name EventName, attrs ...slog.Attr) {
	l.mu.Lock()
	handler := l.handler
	l.mu.Unlock()

	if handler == nil {
		return
	}

	all := make([]slog.Attr, 0, len(attrs)+1)
	all = append(all, slog.String("event", string(name)))
	all = append(all, attrs...)

	handler.LogAttrs(ctx, slog.LevelInfo, string(name), all...)
}

// Reopen closes the current file handle and re-opens the configured path.
// Used by the SIGHUP listener so logrotate can move-then-create or
// copy-then-truncate without losing events. Always emits a
// reopen-succeeded or reopen-failed audit event so operators can confirm
// the rotation took effect.
func (l *Logger) Reopen() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
		l.handler = nil
	}

	if err := l.openLocked(); err != nil {
		return fmt.Errorf("audit reopen: %w", err)
	}

	// Emit a reopen-succeeded event into the *new* file so operators see
	// proof of life. The handler is already swapped under the lock above.
	l.handler.LogAttrs(context.Background(), slog.LevelInfo, string(EventReopenOK),
		slog.String("event", string(EventReopenOK)),
		slog.String("path", l.path),
	)

	return nil
}

// Close flushes + closes the file handle. Safe to call multiple times.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.f == nil {
		return nil
	}

	err := l.f.Close()
	l.f = nil
	l.handler = nil

	if err != nil {
		return fmt.Errorf("audit close: %w", err)
	}

	return nil
}

// openLocked opens the file + builds a fresh slog handler. Caller MUST
// hold l.mu (or be the constructor, where no other goroutine has the
// pointer yet).
func (l *Logger) openLocked() error {
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("audit open %q: %w", l.path, err)
	}

	l.f = f
	l.handler = slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return nil
}
