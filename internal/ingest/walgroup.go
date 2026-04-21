package ingest

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
)

// fsyncSampleCap is the size of the sliding-window ring of successful
// fsync durations exposed via Stats(). 256 × 8 B = 2 KiB resident; at
// ~30 fsyncs/sec the window covers the last ~8 s of fsync activity.
const fsyncSampleCap = 256

// WALAppender is the subset of WALWriter the group syncer needs.
// Defined as an interface so tests can inject a mockable writer that
// exercises the Sync-error path without touching the on-disk tidwall log.
type WALAppender interface {
	Append(ev EnrichedEvent) error
	Sync() error
	CurrentIndex() uint64
}

// ErrGroupSyncerStopped is returned from AppendAndWait after Close.
var ErrGroupSyncerStopped = errors.New("wal group syncer stopped")

// GroupSyncer batches concurrent AppendAndWait calls into a single fsync
// (group commit). No caller's ack returns until the durable Sync has
// completed — honors CLAUDE.md Architecture Rule 4.
//
// Single internal goroutine drains the request channel, calls Append for
// each event, calls Sync once for the whole batch, then signals every
// waiter. fsync errors terminate the process (fsyncgate 2018, LWN 752063):
// pre-4.13 Linux marks failed pages clean on EIO and forgets the error,
// so retrying after a Sync error silently loses data. Postgres responded
// by making fsync failures PANIC; we exit(1) for the same reason.
//
// Throughput: 100ms / 256-event batch covers Filimo's 7K EPS profile in
// ~30 fsyncs/sec (one per ~33ms). Latency cost added to /api/event:
// p50 ~50ms (half the batch interval) within CLAUDE.md's 50ms p95 budget.
type GroupSyncer struct {
	wal      WALAppender
	incoming chan walReq
	out      chan WALEnvelope // downstream channel; consumer reads here

	batchMax int
	interval time.Duration

	auditLog *audit.Logger
	logger   *slog.Logger
	exitFn   func(int) // os.Exit(1) in production; injected in tests

	stopCh chan struct{}
	stopWG sync.WaitGroup

	// fsyncSamples is a ring buffer of successful fsync durations.
	// Producer: the loop goroutine after every successful Sync.
	// Consumer: Stats() under the mutex. Only success durations land
	// here — operators want p99 of the path that actually completes.
	fsyncMu      sync.Mutex
	fsyncRing    []time.Duration
	fsyncWritten uint64 // monotonic count; ring index = (n-1) % cap
}

// WALSyncerStats is the read-only view of GroupSyncer's fsync timing
// surfaced via /healthz. FsyncP99 is the 99th-percentile duration over
// the most recent FsyncSampleCount successful syncs (capped at
// fsyncSampleCap = 256). Zero values mean the ring is empty.
type WALSyncerStats struct {
	FsyncP99         time.Duration
	FsyncSampleCount int
}

// GroupConfig tunes the batch behavior. Defaults match the
// group-commit recommendation in
// .claude/skills/wal-durability-review/SKILL.md (interval ≤ 100ms;
// batch ≤ 256 events).
type GroupConfig struct {
	// BatchMax is the count flush trigger. Default 256.
	BatchMax int
	// Interval is the time flush trigger. Default 100ms.
	Interval time.Duration
	// IncomingBuffer caps the request queue. Larger = more handlers can
	// be in-flight before the loop catches up; smaller = handlers
	// back-pressure earlier. Default 4096 (~7K EPS × 100ms = 700, with
	// headroom for spikes).
	IncomingBuffer int
	// OutBuffer caps the downstream channel that the consumer reads
	// from. Sized larger than IncomingBuffer so a slow consumer
	// back-pressures via the loop's downstream send rather than blocking
	// individual handlers. Default 4096.
	OutBuffer int
}

// WALEnvelope pairs an enriched event with the WAL index assigned to it.
// The consumer uses the index to ack the WAL only after a successful
// ClickHouse insert (Architecture Rule 3 in the wal-durability-review
// skill). Sent on GroupSyncer.Out() after the batch fsync completes.
type WALEnvelope struct {
	Idx uint64
	Ev  EnrichedEvent
}

type walReq struct {
	ev  EnrichedEvent
	ack chan walAck
}

type walAck struct {
	idx uint64
	err error
}

// NewGroupSyncer wraps w with a single fsync goroutine. Caller MUST
// call Close on shutdown to flush the in-flight batch.
func NewGroupSyncer(w WALAppender, cfg GroupConfig, auditLog *audit.Logger, logger *slog.Logger) *GroupSyncer {
	if cfg.BatchMax <= 0 {
		cfg.BatchMax = 256
	}

	if cfg.Interval <= 0 {
		cfg.Interval = 100 * time.Millisecond
	}

	if cfg.IncomingBuffer <= 0 {
		cfg.IncomingBuffer = 4096
	}

	if cfg.OutBuffer <= 0 {
		cfg.OutBuffer = 4096
	}

	g := &GroupSyncer{
		wal:       w,
		incoming:  make(chan walReq, cfg.IncomingBuffer),
		out:       make(chan WALEnvelope, cfg.OutBuffer),
		batchMax:  cfg.BatchMax,
		interval:  cfg.Interval,
		auditLog:  auditLog,
		logger:    logger,
		exitFn:    os.Exit,
		stopCh:    make(chan struct{}),
		fsyncRing: make([]time.Duration, fsyncSampleCap),
	}

	g.stopWG.Add(1)

	go g.loop()

	return g
}

// AppendAndWait writes ev to the WAL and blocks until the containing
// batch has been fsynced. Returns the WAL index. Honors ctx cancellation.
//
// ctx cancellation can race the batch flush: the caller may unblock
// before the durable write completes. The event will still land in the
// WAL (it's already enqueued); the caller just doesn't get the ack.
// Higher layers MUST treat ctx.Err() as a possibly-already-persisted
// signal and not double-process.
func (g *GroupSyncer) AppendAndWait(ctx context.Context, ev EnrichedEvent) (uint64, error) {
	// TODO(perf): pool the per-event ack channel via sync.Pool. At
	// 7K EPS this is 7K alloc/sec; bench-driven optimization deserves
	// its own PR with before/after numbers.
	ack := make(chan walAck, 1)

	select {
	case g.incoming <- walReq{ev: ev, ack: ack}:
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-g.stopCh:
		return 0, ErrGroupSyncerStopped
	}

	select {
	case a := <-ack:
		return a.idx, a.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Out is the read-only side of the downstream channel the consumer
// drains. Each WALEnvelope is sent ONLY after the containing batch has
// been fsynced, so a consumer that reads from Out can trust the WAL has
// the event durably. The channel closes when the loop exits via Close.
func (g *GroupSyncer) Out() <-chan WALEnvelope { return g.out }

// Close stops the loop after flushing the in-flight batch. Safe to call
// multiple times — second call is a no-op.
func (g *GroupSyncer) Close() {
	select {
	case <-g.stopCh:
		return
	default:
		close(g.stopCh)
	}

	g.stopWG.Wait()
}

func (g *GroupSyncer) loop() {
	defer g.stopWG.Done()
	// Close the downstream channel after final flush so the consumer
	// sees a clean EOF on shutdown (range loop exits naturally).
	defer close(g.out)

	pending := make([]walReq, 0, g.batchMax)

	// Use NewTimer + Reset rather than time.After in the select — every
	// time.After call leaks a Timer until it fires (golang-concurrency
	// principle 8). nil-timer-channel is the "no timer set" sentinel
	// (selecting on a nil channel never fires).
	flushTimer := time.NewTimer(time.Hour)
	flushTimer.Stop()

	timerActive := false

	for {
		select {
		case <-g.stopCh:
			g.flush(pending)

			return
		case req := <-g.incoming:
			pending = append(pending, req)

			if len(pending) >= g.batchMax {
				if timerActive {
					// Go 1.23+ pattern: Stop + non-blocking drain.
					// Stop returns false when the timer already fired
					// AND was drained, OR when it was never armed; the
					// select-with-default avoids the hang the older
					// "if !Stop() { <-C }" pattern can hit on re-entry.
					flushTimer.Stop()

					select {
					case <-flushTimer.C:
					default:
					}

					timerActive = false
				}

				g.flush(pending)
				pending = pending[:0]
			} else if !timerActive {
				flushTimer.Reset(g.interval)

				timerActive = true
			}
		case <-flushTimer.C:
			timerActive = false

			if len(pending) > 0 {
				g.flush(pending)
				pending = pending[:0]
			}
		}
	}
}

// flush appends every event in batch to the WAL, calls Sync once, then
// signals every waiter. Sync errors terminate the process — never
// log-and-continue on an fsync failure (fsyncgate 2018).
func (g *GroupSyncer) flush(batch []walReq) {
	if len(batch) == 0 {
		return
	}

	// TODO(perf): hoist these slices to GroupSyncer fields and reuse
	// across flushes (single goroutine, no sync needed). At BatchMax=256
	// × 30 fsyncs/sec = 7680 alloc/sec for the two slices alone.
	appendErrs := make([]error, len(batch))
	indices := make([]uint64, len(batch))

	for i := range batch {
		if err := g.wal.Append(batch[i].ev); err != nil {
			appendErrs[i] = err

			continue
		}

		indices[i] = g.wal.CurrentIndex()
	}

	syncStart := time.Now()
	if syncErr := g.wal.Sync(); syncErr != nil {
		// Pre-4.13 Linux fsync marks failed pages clean on EIO. Retrying
		// after a Sync error silently loses data — the only safe response
		// is process termination (orchestrator restarts; restart re-opens
		// the WAL fresh). Signal every waiter with the error so handlers
		// can return 503 instead of hanging on the unbuffered ack.
		for _, req := range batch {
			req.ack <- walAck{err: syncErr}

			close(req.ack)
		}

		g.fatal(syncErr)

		return
	}

	g.recordFsync(time.Since(syncStart))

	// Two-step signal-then-fanout. Handlers care about durability
	// (Sync just succeeded) so unblock them first; the consumer can
	// catch up without blocking handler ack latency.
	for i, req := range batch {
		req.ack <- walAck{idx: indices[i], err: appendErrs[i]}

		close(req.ack)
	}

	// Push successful events to the consumer. Blocking send so a slow
	// consumer back-pressures the loop (and eventually the handlers via
	// the upstream incoming chan) instead of dropping events that are
	// already durable in the WAL.
	for i := range batch {
		if appendErrs[i] != nil {
			continue
		}

		select {
		case g.out <- WALEnvelope{Idx: indices[i], Ev: batch[i].ev}:
		case <-g.stopCh:
			// Shutting down; consumer will pick up via WAL replay on next
			// boot. Event is durable (Sync done above).
			g.logger.Warn("wal group syncer dropping post-fsync delivery on shutdown",
				"idx", indices[i])
		}
	}
}

// recordFsync stores d in the success-only ring buffer. Producer is the
// loop goroutine; lock contention with Stats() is irrelevant at the
// ~30 fsyncs/sec the syncer runs at.
func (g *GroupSyncer) recordFsync(d time.Duration) {
	g.fsyncMu.Lock()
	defer g.fsyncMu.Unlock()

	g.fsyncRing[g.fsyncWritten%uint64(len(g.fsyncRing))] = d
	g.fsyncWritten++
}

// Stats returns the most recent fsync timing window. Safe to call from
// any goroutine. /healthz reads this on every request; the sort over
// 256 entries is sub-microsecond on modern hardware.
func (g *GroupSyncer) Stats() WALSyncerStats {
	g.fsyncMu.Lock()

	// Cap the visible sample count by the ring capacity, then convert.
	// Doing the comparison in uint64 keeps gosec G115 happy: after the
	// min, n is always within int range (fsyncSampleCap = 256).
	written := g.fsyncWritten

	cap64 := uint64(len(g.fsyncRing))
	if written > cap64 {
		written = cap64
	}

	n := int(written) //nolint:gosec // bounded above by len(g.fsyncRing) = fsyncSampleCap = 256.
	if n == 0 {
		g.fsyncMu.Unlock()

		return WALSyncerStats{}
	}

	samples := make([]time.Duration, n)
	copy(samples, g.fsyncRing[:n])

	g.fsyncMu.Unlock()

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	// Index of the 99th percentile. n=1 → 0; n=100 → 99; n=256 → 253.
	// Subtract 1 from the ceiling so the result is always inside bounds.
	p99Idx := (n*99 + 99) / 100
	if p99Idx >= n {
		p99Idx = n - 1
	}

	return WALSyncerStats{
		FsyncP99:         samples[p99Idx],
		FsyncSampleCount: n,
	}
}

func (g *GroupSyncer) fatal(err error) {
	g.logger.Error("wal fsync failed; terminating to preserve data integrity",
		"err", err.Error())

	emitAudit(context.Background(), g.auditLog, audit.EventWALSyncFailed,
		slog.String("err", err.Error()))

	g.exitFn(1)
}
