// Package alerts emits operational alerts to an append-only JSONL file
// sink at the path configured by `alerts.sink_path`. The sink is the
// backend for Phase 6-polish-5's Notice primitive (toast / persistent
// notice / status line) — the UI reads via a future
// `GET /api/ops/alerts` endpoint; Phase 8 only ships the emitter.
//
// Two kinds of events land here:
//
//   - Enter-band  (severity warn|critical, resolved=false) — condition
//     crosses a threshold (WAL >80%, CH ping fails, disk >85%, cert <30d).
//   - Exit-band   (severity info,          resolved=true)  — condition
//     clears. The Notice UI auto-dismisses the paired persistent notice.
//
// Every emit site must debounce: fire once per threshold crossing, NOT
// every sample. The package provides a BandTracker helper that captures
// the state transition so callers don't re-implement the debounce.
//
// Design is deliberately twin to internal/audit/log.go — same
// open/reopen/close semantics, same 0640 perm, same JSONL shape. The
// file stays distinct because audit records completed actions (auth,
// admin, ingest milestones) while alerts records "ops should know
// NOW"; v1.1 adds remote fanout (Telegram / syslog) to alerts only.
package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

// Severity is the log-level a given alert carries.
type Severity string

// Severity values — "info" is reserved for the recover/resolved side of
// a paired enter/exit alert; "warn" and "critical" map to the two
// visible bands the Notice UI surfaces.
const (
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityCritical Severity = "critical"
)

// Sink writes alerts to a JSONL file. Safe for concurrent use.
type Sink struct {
	path    string
	hostTag string

	mu      sync.Mutex
	f       *os.File
	handler *slog.Logger
}

// New opens (or creates) the alerts sink at path. A nil Sink (path == "")
// is valid — every emit becomes a no-op. That's the "file sink disabled"
// posture the config uses to opt out entirely.
func New(path, hostTag string) (*Sink, error) {
	if path == "" {
		return nil, nil //nolint:nilnil // intentional: no-op sink for disabled config
	}

	s := &Sink{path: path, hostTag: hostTag}

	if err := s.openLocked(); err != nil {
		return nil, err
	}

	return s, nil
}

// Emit writes one alert record. Safe against a nil receiver (the
// disabled-sink case). Extra attrs are appended after the core fields
// (time / alert / severity / resolved / host).
func (s *Sink) Emit(ctx context.Context, name string, sev Severity, resolved bool, attrs ...slog.Attr) {
	if s == nil {
		return
	}

	s.mu.Lock()
	handler := s.handler
	s.mu.Unlock()

	if handler == nil {
		return
	}

	all := make([]slog.Attr, 0, len(attrs)+4)
	all = append(all,
		slog.String("alert", name),
		slog.String("severity", string(sev)),
		slog.Bool("resolved", resolved),
	)

	if s.hostTag != "" {
		all = append(all, slog.String("host", s.hostTag))
	}

	all = append(all, attrs...)

	handler.LogAttrs(ctx, slog.LevelInfo, name, all...)
}

// Reopen swaps in a fresh file handle — same contract as
// audit.Logger.Reopen, used by the SIGHUP fan-out when logrotate
// moves the file.
func (s *Sink) Reopen() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.f != nil {
		_ = s.f.Close()
		s.f = nil
		s.handler = nil
	}

	if err := s.openLocked(); err != nil {
		return fmt.Errorf("alerts reopen: %w", err)
	}

	return nil
}

// Close flushes the handle. Safe to call multiple times.
func (s *Sink) Close() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.f == nil {
		return nil
	}

	err := s.f.Close()
	s.f = nil
	s.handler = nil

	if err != nil {
		return fmt.Errorf("alerts close: %w", err)
	}

	return nil
}

func (s *Sink) openLocked() error {
	if s.path == "" {
		return errors.New("alerts: empty path")
	}

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640) //nolint:gosec // 0640 matches audit.Logger convention; log-shipper group reads
	if err != nil {
		return fmt.Errorf("alerts open %q: %w", s.path, err)
	}

	s.f = f
	s.handler = slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return nil
}
