package ingest

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
)

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

	batchMax int
	interval time.Duration

	auditLog *audit.Logger
	logger   *slog.Logger
	exitFn   func(int) // os.Exit(1) in production; injected in tests

	stopCh chan struct{}
	stopWG sync.WaitGroup
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

	g := &GroupSyncer{
		wal:      w,
		incoming: make(chan walReq, cfg.IncomingBuffer),
		batchMax: cfg.BatchMax,
		interval: cfg.Interval,
		auditLog: auditLog,
		logger:   logger,
		exitFn:   os.Exit,
		stopCh:   make(chan struct{}),
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

	for i, req := range batch {
		req.ack <- walAck{idx: indices[i], err: appendErrs[i]}

		close(req.ack)
	}
}

func (g *GroupSyncer) fatal(err error) {
	g.logger.Error("wal fsync failed; terminating to preserve data integrity",
		"err", err.Error())

	emitAudit(context.Background(), g.auditLog, audit.EventWALSyncFailed,
		slog.String("err", err.Error()))

	g.exitFn(1)
}
