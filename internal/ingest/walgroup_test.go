package ingest

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeWAL is a WALAppender stub that records Append + Sync calls and can
// be programmed to fail. Per-call serialization mirrors the production
// WALWriter's mutex behavior so race-detector runs match real semantics.
type fakeWAL struct {
	mu sync.Mutex

	appended  []EnrichedEvent
	idx       uint64
	syncCount int

	appendErr error // returned from every Append until cleared
	syncErr   error // returned from every Sync until cleared

	syncDelay time.Duration // sleep inside Sync; lets tests force a slow fsync
}

func (f *fakeWAL) Append(ev EnrichedEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.appendErr != nil {
		return f.appendErr
	}

	f.appended = append(f.appended, ev)
	f.idx++

	return nil
}

func (f *fakeWAL) Sync() error {
	f.mu.Lock()
	delay := f.syncDelay
	err := f.syncErr
	f.syncCount++
	f.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}

	return err
}

func (f *fakeWAL) CurrentIndex() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.idx
}

func (f *fakeWAL) snapshot() (events []EnrichedEvent, syncs int, idx uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]EnrichedEvent, len(f.appended))
	copy(out, f.appended)

	return out, f.syncCount, f.idx
}

func newGroupSyncerForTest(t *testing.T, w WALAppender, cfg GroupConfig) (*GroupSyncer, *atomic.Int32) {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	exitCalls := &atomic.Int32{}

	g := NewGroupSyncer(w, cfg, nil, logger)
	g.exitFn = func(_ int) { exitCalls.Add(1) }

	t.Cleanup(g.Close)

	return g, exitCalls
}

func TestGroupSyncer_SingleAppendReturnsIndex(t *testing.T) {
	t.Parallel()

	w := &fakeWAL{}
	g, _ := newGroupSyncerForTest(t, w, GroupConfig{Interval: 5 * time.Millisecond})

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	idx, err := g.AppendAndWait(ctx, EnrichedEvent{SiteID: 1, Pathname: "/test"})
	if err != nil {
		t.Fatalf("AppendAndWait: %v", err)
	}

	if idx != 1 {
		t.Errorf("idx = %d; want 1", idx)
	}

	events, syncs, _ := w.snapshot()
	if len(events) != 1 {
		t.Errorf("appended = %d; want 1", len(events))
	}

	if syncs != 1 {
		t.Errorf("syncs = %d; want 1", syncs)
	}
}

// Group commit invariant: many concurrent appenders complete after
// exactly ONE Sync call, not one Sync per append.
func TestGroupSyncer_GroupCommit(t *testing.T) {
	t.Parallel()

	const concurrent = 64

	w := &fakeWAL{}
	g, _ := newGroupSyncerForTest(t, w, GroupConfig{
		BatchMax: concurrent * 2, // ensure timer fires before batch fills
		Interval: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	results := make([]uint64, concurrent)
	errs := make([]error, concurrent)

	for i := range concurrent {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			//nolint:gosec // i is bounded by `concurrent` const = 64; well inside uint32.
			idx, err := g.AppendAndWait(ctx, EnrichedEvent{SiteID: uint32(i)})
			results[i] = idx
			errs[i] = err
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}

	_, syncs, idx := w.snapshot()
	if syncs != 1 {
		t.Errorf("syncs = %d; want 1 (group commit failed)", syncs)
	}

	if idx != concurrent {
		t.Errorf("final idx = %d; want %d", idx, concurrent)
	}

	// Every result is unique and inside [1, concurrent].
	seen := make(map[uint64]bool)

	for _, r := range results {
		if r < 1 || r > concurrent {
			t.Errorf("idx %d out of range [1, %d]", r, concurrent)
		}

		if seen[r] {
			t.Errorf("duplicate idx %d", r)
		}

		seen[r] = true
	}
}

// BatchMax must trigger an immediate flush without waiting for the timer.
func TestGroupSyncer_FlushOnBatchMax(t *testing.T) {
	t.Parallel()

	const batchMax = 8

	w := &fakeWAL{}
	g, _ := newGroupSyncerForTest(t, w, GroupConfig{
		BatchMax: batchMax,
		Interval: 5 * time.Second, // long enough that only batch-max can trigger
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	for range batchMax {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _ = g.AppendAndWait(ctx, EnrichedEvent{SiteID: 1})
		}()
	}

	wg.Wait()

	_, syncs, _ := w.snapshot()
	if syncs != 1 {
		t.Errorf("syncs = %d; want 1 (BatchMax should have triggered immediate flush)", syncs)
	}
}

// Sync error must terminate the process. exitFn is injected in tests.
func TestGroupSyncer_SyncErrorTerminates(t *testing.T) {
	t.Parallel()

	w := &fakeWAL{syncErr: errors.New("simulated EIO")}
	g, exits := newGroupSyncerForTest(t, w, GroupConfig{Interval: 5 * time.Millisecond})

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	_, err := g.AppendAndWait(ctx, EnrichedEvent{SiteID: 1})
	if err == nil {
		t.Fatal("expected sync error to surface to the waiter")
	}

	if !errors.Is(err, w.syncErr) {
		t.Errorf("err = %v; want %v", err, w.syncErr)
	}

	// Give the goroutine a moment to call exitFn after notifying waiters.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if exits.Load() == 1 {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Errorf("exitFn called %d times; want 1", exits.Load())
}

// Append error on one event must not block the rest of the batch.
func TestGroupSyncer_PartialAppendErrorDoesNotBlockBatch(t *testing.T) {
	t.Parallel()

	w := &fakeWAL{appendErr: errors.New("disk full")}
	g, _ := newGroupSyncerForTest(t, w, GroupConfig{Interval: 5 * time.Millisecond})

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	_, err := g.AppendAndWait(ctx, EnrichedEvent{SiteID: 1})
	if err == nil {
		t.Fatal("expected append error to surface")
	}

	if !errors.Is(err, w.appendErr) {
		t.Errorf("err = %v; want disk full", err)
	}

	_, syncs, _ := w.snapshot()
	if syncs != 1 {
		t.Errorf("syncs = %d; want 1 (Sync still runs even when Appends failed)", syncs)
	}
}

// Cancelled context must release the waiter promptly even when the
// fsync is still in flight.
func TestGroupSyncer_ContextCancelReleases(t *testing.T) {
	t.Parallel()

	w := &fakeWAL{syncDelay: 100 * time.Millisecond}
	g, _ := newGroupSyncerForTest(t, w, GroupConfig{Interval: 5 * time.Millisecond})

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() {
		_, err := g.AppendAndWait(ctx, EnrichedEvent{SiteID: 1})
		done <- err
	}()

	// Let the request enter the channel + the loop start the flush.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v; want context.Canceled", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("AppendAndWait did not release on ctx cancel")
	}
}

// AppendAndWait after Close must not hang.
func TestGroupSyncer_AppendAfterCloseFails(t *testing.T) {
	t.Parallel()

	w := &fakeWAL{}
	g, _ := newGroupSyncerForTest(t, w, GroupConfig{Interval: 5 * time.Millisecond})

	g.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, err := g.AppendAndWait(ctx, EnrichedEvent{SiteID: 1})
	if err == nil {
		t.Fatal("expected error from AppendAndWait after Close")
	}
}

// Close is idempotent.
func TestGroupSyncer_CloseIdempotent(t *testing.T) {
	t.Parallel()

	w := &fakeWAL{}
	g := NewGroupSyncer(w, GroupConfig{Interval: 5 * time.Millisecond}, nil, slog.New(slog.DiscardHandler))

	g.Close()
	g.Close() // second close MUST NOT panic
}

// Stats reports zero values when no flush has run yet.
func TestGroupSyncer_StatsEmptyBeforeAnyFlush(t *testing.T) {
	t.Parallel()

	w := &fakeWAL{}
	g, _ := newGroupSyncerForTest(t, w, GroupConfig{Interval: 5 * time.Millisecond})

	stats := g.Stats()
	if stats.FsyncSampleCount != 0 {
		t.Errorf("FsyncSampleCount = %d; want 0", stats.FsyncSampleCount)
	}

	if stats.FsyncP99 != 0 {
		t.Errorf("FsyncP99 = %v; want 0", stats.FsyncP99)
	}
}

// Stats() reports a plausible p99 after observed fsync activity. The
// fakeWAL injects a known syncDelay, so the recorded duration must be
// ≥ that delay (some overhead is expected from the time.Now bracketing
// + sleep granularity). We bound it loosely to keep the test fast and
// non-flaky; the exact value isn't load-bearing.
func TestGroupSyncer_StatsReportsP99AfterFlushes(t *testing.T) {
	t.Parallel()

	const syncDelay = 5 * time.Millisecond

	w := &fakeWAL{syncDelay: syncDelay}
	g, _ := newGroupSyncerForTest(t, w, GroupConfig{
		BatchMax: 1, // one event per flush ⇒ one Sync per AppendAndWait
		Interval: time.Hour,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	const flushes = 10

	for range flushes {
		_, err := g.AppendAndWait(ctx, EnrichedEvent{SiteID: 1})
		if err != nil {
			t.Fatalf("AppendAndWait: %v", err)
		}
	}

	stats := g.Stats()
	if stats.FsyncSampleCount != flushes {
		t.Errorf("FsyncSampleCount = %d; want %d", stats.FsyncSampleCount, flushes)
	}

	if stats.FsyncP99 < syncDelay {
		t.Errorf("FsyncP99 = %v; want >= %v (the injected sync delay)", stats.FsyncP99, syncDelay)
	}

	if stats.FsyncP99 > syncDelay+50*time.Millisecond {
		t.Errorf("FsyncP99 = %v; suspiciously slow (delay was %v)", stats.FsyncP99, syncDelay)
	}
}

// Sync errors do NOT contribute to the success-only sample ring.
func TestGroupSyncer_StatsExcludesSyncErrors(t *testing.T) {
	t.Parallel()

	w := &fakeWAL{syncErr: errors.New("simulated EIO")}
	g, _ := newGroupSyncerForTest(t, w, GroupConfig{Interval: 5 * time.Millisecond})

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	_, _ = g.AppendAndWait(ctx, EnrichedEvent{SiteID: 1})

	// Allow the loop a moment to complete the flush and (in production)
	// call exit; we injected the exit shim via newGroupSyncerForTest so
	// it just bumps a counter.
	time.Sleep(20 * time.Millisecond)

	stats := g.Stats()
	if stats.FsyncSampleCount != 0 {
		t.Errorf("FsyncSampleCount = %d; want 0 (error syncs must not record)", stats.FsyncSampleCount)
	}
}

// Ring buffer wraps cleanly past fsyncSampleCap; the sample count caps
// at the ring size and reflects the most recent N successful syncs.
func TestGroupSyncer_StatsRingWrapsAtCap(t *testing.T) {
	t.Parallel()

	w := &fakeWAL{}
	g, _ := newGroupSyncerForTest(t, w, GroupConfig{
		BatchMax: 1,
		Interval: time.Hour,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Fire more events than the ring holds; the ring must cap at fsyncSampleCap.
	for range fsyncSampleCap + 50 {
		if _, err := g.AppendAndWait(ctx, EnrichedEvent{SiteID: 1}); err != nil {
			t.Fatalf("AppendAndWait: %v", err)
		}
	}

	stats := g.Stats()
	if stats.FsyncSampleCount != fsyncSampleCap {
		t.Errorf("FsyncSampleCount = %d; want %d (ring should cap)", stats.FsyncSampleCount, fsyncSampleCap)
	}
}

// Close flushes any in-flight batch.
func TestGroupSyncer_CloseFlushesPending(t *testing.T) {
	t.Parallel()

	w := &fakeWAL{}
	g, _ := newGroupSyncerForTest(t, w, GroupConfig{
		BatchMax: 100,
		Interval: 10 * time.Second, // never times out during the test
	})

	const events = 5

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	// Send events but do NOT wait for ack — close should flush anyway.
	for range events {
		go func() { _, _ = g.AppendAndWait(ctx, EnrichedEvent{SiteID: 1}) }()
	}

	time.Sleep(20 * time.Millisecond) // let requests reach the loop

	g.Close()

	appended, syncs, _ := w.snapshot()
	if len(appended) != events {
		t.Errorf("appended = %d; want %d (Close should flush pending batch)", len(appended), events)
	}

	if syncs != 1 {
		t.Errorf("syncs = %d; want 1 (Close should fsync once)", syncs)
	}
}
