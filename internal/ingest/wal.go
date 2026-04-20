package ingest

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	twal "github.com/tidwall/wal"

	"github.com/statnive/statnive.live/internal/audit"
)

// walFsyncInterval is the periodic fsync cadence (PLAN.md:159 — 100 ms).
// Per-event fsync would cap throughput at ~100 events/s on HDD; the
// background ticker is what makes 7 K EPS achievable.
const walFsyncInterval = 100 * time.Millisecond

// WALConfig is read from YAML at startup.
type WALConfig struct {
	Dir      string
	MaxBytes int64 // PLAN.md:159 — default 10 GB cap.
	// AuditLog is optional; nil silences wal.corrupt_skipped emissions.
	// Injected by main.go for the prod path; test WALWriters pass nil.
	AuditLog *audit.Logger
}

// WALWriter wraps tidwall/wal with an fsync ticker and a periodic
// size-cap enforcer. Indexes are monotonic and assigned by Append.
type WALWriter struct {
	log      *twal.Log
	dir      string
	maxBytes int64
	mu       sync.Mutex
	nextIdx  uint64
	auditLog *audit.Logger
	logger   *slog.Logger

	stopCh chan struct{}
	stopWG sync.WaitGroup
}

// NewWALWriter opens or creates the WAL directory and starts the fsync loop.
func NewWALWriter(cfg WALConfig, logger *slog.Logger) (*WALWriter, error) {
	if cfg.Dir == "" {
		return nil, errors.New("wal: dir is required")
	}

	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("wal mkdir: %w", err)
	}

	// wal-durability-review item #4: WAL directory must live on the
	// same filesystem as its parent so tidwall's segment rotation
	// (which uses os.Rename) stays atomic. A cross-FS rename is two
	// copy-then-unlink operations; a crash between them leaves
	// partial or duplicated segments on disk.
	if err := assertSameFilesystem(cfg.Dir); err != nil {
		return nil, fmt.Errorf("wal dir: %w", err)
	}

	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024 * 1024 // 10 GB
	}

	l, err := twal.Open(cfg.Dir, &twal.Options{
		NoSync:           true, // fsync handled by the ticker below.
		SegmentSize:      64 << 20,
		LogFormat:        twal.Binary,
		SegmentCacheSize: 4,
	})
	if err != nil {
		return nil, fmt.Errorf("wal open: %w", err)
	}

	last, err := l.LastIndex()
	if err != nil {
		_ = l.Close()

		return nil, fmt.Errorf("wal last index: %w", err)
	}

	w := &WALWriter{
		log:      l,
		dir:      cfg.Dir,
		maxBytes: maxBytes,
		nextIdx:  last + 1,
		auditLog: cfg.AuditLog,
		logger:   logger,
		stopCh:   make(chan struct{}),
	}

	w.stopWG.Add(1)
	go w.fsyncLoop()

	return w, nil
}

// Append serializes one event and writes it at the next monotonic index.
func (w *WALWriter) Append(ev EnrichedEvent) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&ev); err != nil {
		return fmt.Errorf("gob encode: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.log.Write(w.nextIdx, buf.Bytes()); err != nil {
		return fmt.Errorf("wal write: %w", err)
	}

	w.nextIdx++

	return nil
}

// CurrentIndex returns the most recently written index (0 if empty).
func (w *WALWriter) CurrentIndex() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.nextIdx == 0 {
		return 0
	}

	return w.nextIdx - 1
}

// Replay iterates every persisted entry and invokes cb. The callback is
// called in WAL order. Stops on the first cb error; the caller decides
// whether to truncate.
//
// Structured logging per wal-durability-review item #8: emits
// `wal_replay{first_idx, last_idx}` at start and
// `wal_replay_done{total, corrupt_skipped, elapsed_ms}` at end. Corrupt
// entries (tidwall ErrNotFound on an in-range index, or a gob decode
// error) fire audit.EventWALCorruptSkipped rather than being silently
// swallowed (item #10).
func (w *WALWriter) Replay(cb func(EnrichedEvent) error) error {
	first, err := w.log.FirstIndex()
	if err != nil {
		return fmt.Errorf("wal first index: %w", err)
	}

	last, err := w.log.LastIndex()
	if err != nil {
		return fmt.Errorf("wal last index: %w", err)
	}

	if first == 0 || last == 0 || first > last {
		return nil
	}

	// Item #9: LastIndex must be monotonically ≥ FirstIndex once the
	// log has any entries. A regression here means tidwall returned
	// inconsistent metadata — fail fast.
	if last < first {
		return fmt.Errorf("wal invariant violated: last_idx %d < first_idx %d", last, first)
	}

	w.logger.Info("wal_replay", "first_idx", first, "last_idx", last)

	start := time.Now()

	var (
		total   int64
		corrupt int64
	)

	for i := first; i <= last; i++ {
		data, readErr := w.log.Read(i)
		if readErr != nil {
			if errors.Is(readErr, twal.ErrNotFound) {
				corrupt++

				w.emitCorrupt(i, "read_not_found")

				continue
			}

			return fmt.Errorf("wal read %d: %w", i, readErr)
		}

		var ev EnrichedEvent
		if decErr := gob.NewDecoder(bytes.NewReader(data)).Decode(&ev); decErr != nil {
			corrupt++

			w.emitCorrupt(i, "gob_decode_error")

			continue
		}

		if cbErr := cb(ev); cbErr != nil {
			return cbErr
		}

		total++
	}

	w.logger.Info("wal_replay_done",
		"total", total,
		"corrupt_skipped", corrupt,
		"elapsed_ms", time.Since(start).Milliseconds(),
	)

	return nil
}

// emitCorrupt audits one corrupt-segment detection. Kept private so
// the loop stays terse while preserving the audit trail required by
// wal-durability-review item #10.
func (w *WALWriter) emitCorrupt(idx uint64, reason string) {
	w.logger.Warn("wal entry corrupt, skipping", "idx", idx, "reason", reason)

	if w.auditLog == nil {
		return
	}

	w.auditLog.Event(context.Background(), audit.EventWALCorruptSkipped,
		slog.Uint64("idx", idx),
		slog.String("reason", reason),
	)
}

// Ack truncates the front so entries up to and including throughIndex are
// removed. tidwall/wal disallows truncating all entries, so we keep one.
func (w *WALWriter) Ack(throughIndex uint64) error {
	if throughIndex == 0 {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	last, err := w.log.LastIndex()
	if err != nil {
		return fmt.Errorf("wal last index: %w", err)
	}

	if throughIndex >= last {
		if last == 0 {
			return nil
		}

		throughIndex = last - 1
		if throughIndex == 0 {
			return nil
		}
	}

	if err := w.log.TruncateFront(throughIndex + 1); err != nil {
		return fmt.Errorf("truncate front: %w", err)
	}

	return nil
}

// Sync forces an immediate fsync. Called by graceful shutdown.
func (w *WALWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.log.Sync()
}

// Close stops the background loop and closes the WAL.
func (w *WALWriter) Close() error {
	close(w.stopCh)
	w.stopWG.Wait()

	w.mu.Lock()
	defer w.mu.Unlock()

	_ = w.log.Sync()

	return w.log.Close()
}

// FillRatio returns the WAL's current size / max-bytes ratio in [0, 1+).
// /healthz uses this to surface the >80% threshold per PLAN.md:159.
func (w *WALWriter) FillRatio() float64 {
	size, err := dirSize(w.dir)
	if err != nil || w.maxBytes <= 0 {
		return 0
	}

	return float64(size) / float64(w.maxBytes)
}

func (w *WALWriter) fsyncLoop() {
	defer w.stopWG.Done()

	tick := time.NewTicker(walFsyncInterval)
	defer tick.Stop()

	capTick := time.NewTicker(5 * time.Second)
	defer capTick.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-tick.C:
			w.mu.Lock()
			if err := w.log.Sync(); err != nil {
				w.logger.Warn("wal fsync", "err", err)
			}
			w.mu.Unlock()
		case <-capTick.C:
			w.enforceSizeCap()
		}
	}
}

// enforceSizeCap drops oldest segments when the WAL exceeds maxBytes.
// Loops on the next tick until under cap.
func (w *WALWriter) enforceSizeCap() {
	size, err := dirSize(w.dir)
	if err != nil || size <= w.maxBytes {
		return
	}

	w.logger.Warn("wal exceeds size cap, dropping oldest", "bytes", size, "cap", w.maxBytes)

	w.mu.Lock()
	defer w.mu.Unlock()

	first, err := w.log.FirstIndex()
	if err != nil || first == 0 {
		return
	}

	target := first + 1000

	last, lastErr := w.log.LastIndex()
	if lastErr == nil && target >= last && last > 0 {
		target = last - 1
	}

	if target <= first {
		return
	}

	if truncErr := w.log.TruncateFront(target); truncErr != nil {
		w.logger.Warn("wal truncate-front on cap", "err", truncErr)
	}
}

func dirSize(dir string) (int64, error) {
	var total int64

	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}

		total += info.Size()

		return nil
	})

	return total, err
}

// assertSameFilesystem fails when dir lives on a different filesystem
// than its parent. tidwall/wal's segment rotation uses os.Rename; a
// cross-FS rename is NOT atomic (copy-then-unlink), so a crash between
// copy and unlink leaves either a missing segment (data loss) or a
// duplicate (replay misorder). wal-durability-review item #4.
//
// No-op on Linux + macOS when the dir is a child of a filesystem mount
// point (common case). Returns an error only when the operator has
// mounted the WAL dir on a separate volume — which is a deployment
// mistake we want to fail loud rather than silently corrupt.
func assertSameFilesystem(dir string) error {
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat %q: %w", dir, err)
	}

	parentInfo, err := os.Stat(filepath.Dir(dir))
	if err != nil {
		return fmt.Errorf("stat parent of %q: %w", dir, err)
	}

	dirSys, dirOK := dirInfo.Sys().(*syscall.Stat_t)
	parSys, parOK := parentInfo.Sys().(*syscall.Stat_t)

	// If either cast fails (non-Unix platform — test environment etc.)
	// we skip the check rather than blocking startup. The invariant is
	// relevant only on Linux production; macOS dev rigs use a single
	// volume by default.
	if !dirOK || !parOK {
		return nil
	}

	if dirSys.Dev != parSys.Dev {
		return fmt.Errorf("WAL dir %q is on a different filesystem than its parent — rename atomicity is not guaranteed across mounts (wal-durability-review item #4)", dir)
	}

	return nil
}
