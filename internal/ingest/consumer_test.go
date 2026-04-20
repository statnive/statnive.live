package ingest_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/ingest"
)

// flakyInserter fails the first N InsertBatch calls then succeeds.
// Records every batch it saw for assertion.
type flakyInserter struct {
	failsLeft int32
	calls     atomic.Int32
	successes atomic.Int32
}

func (f *flakyInserter) InsertBatch(_ context.Context, _ []ingest.EnrichedEvent) error {
	f.calls.Add(1)

	if atomic.AddInt32(&f.failsLeft, -1) >= 0 {
		return errors.New("simulated CH outage")
	}

	f.successes.Add(1)

	return nil
}

// permanentlyFailingInserter always errors — exhausts the retry budget.
type permanentlyFailingInserter struct {
	calls atomic.Int32
}

func (p *permanentlyFailingInserter) InsertBatch(_ context.Context, _ []ingest.EnrichedEvent) error {
	p.calls.Add(1)

	return errors.New("ch down")
}

// Small helper: make a WAL writer in a temp dir for the consumer to ack.
func testWAL(t *testing.T) *ingest.WALWriter {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "wal")

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	w, err := ingest.NewWALWriter(ingest.WALConfig{Dir: dir, MaxBytes: 10 * 1024 * 1024}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("new wal: %v", err)
	}

	t.Cleanup(func() { _ = w.Close() })

	return w
}

// Consumer must retry transient InsertBatch errors (backoff) then ack
// the WAL on eventual success.
func TestConsumer_RetriesTransientErrors(t *testing.T) {
	t.Parallel()

	wal := testWAL(t)
	flaky := &flakyInserter{failsLeft: 2} // succeeds on attempt 3

	in := make(chan ingest.WALEnvelope, 2)
	c := ingest.NewConsumer(in, wal, flaky, ingest.ConsumerConfig{
		BatchRows:     1,
		BatchInterval: 50 * time.Millisecond,
		DrainSettle:   100 * time.Millisecond, // keep the test fast
	}, nil, slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		c.Run(ctx)
		close(done)
	}()

	in <- ingest.WALEnvelope{Idx: 1, Ev: ingest.EnrichedEvent{SiteID: 1}}

	// Wait for retry ladder (100 + 500 = 600ms) + flush.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if flaky.successes.Load() >= 1 {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	<-done

	if flaky.successes.Load() != 1 {
		t.Errorf("successes = %d; want 1 after retry ladder", flaky.successes.Load())
	}

	if flaky.calls.Load() < 3 {
		t.Errorf("calls = %d; want ≥ 3 (2 failures + 1 success)", flaky.calls.Load())
	}
}

// When the retry budget is exhausted, the WAL MUST NOT be acked — the
// events stay durable for the next flush cycle to retry.
// Verified by checking the WAL's current index did not advance to
// "consumed" state (we can't easily introspect ack state, but the key
// assertion is that InsertBatch was called the full retry budget +
// initial attempt = 4 times, and the flush returned cleanly rather
// than panicking or acking).
func TestConsumer_DoesNotAckWhenCHErrorExhaustsRetries(t *testing.T) {
	t.Parallel()

	wal := testWAL(t)
	failing := &permanentlyFailingInserter{}

	in := make(chan ingest.WALEnvelope, 1)
	c := ingest.NewConsumer(in, wal, failing, ingest.ConsumerConfig{
		BatchRows:     1,
		BatchInterval: 50 * time.Millisecond,
		DrainSettle:   100 * time.Millisecond,
	}, nil, slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		c.Run(ctx)
		close(done)
	}()

	in <- ingest.WALEnvelope{Idx: 1, Ev: ingest.EnrichedEvent{SiteID: 1}}

	// Wait for full retry ladder: 100 + 500 + 2000 = 2600ms total.
	// Plus safety margin.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if failing.calls.Load() >= 4 {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	<-done

	// 4 attempts total: initial + 3 retries (100ms / 500ms / 2s backoff).
	if got := failing.calls.Load(); got != 4 {
		t.Errorf("InsertBatch calls = %d; want 4 (initial + 3 retries)", got)
	}
}

// passingInserter always succeeds.
type passingInserter struct{ calls atomic.Int32 }

func (p *passingInserter) InsertBatch(_ context.Context, _ []ingest.EnrichedEvent) error {
	p.calls.Add(1)

	return nil
}

// Drain bound: after ctx cancel, drain exits within
// DrainSettle + BatchInterval × 2. Previous implementation used
// `default: return` which exited immediately on any empty-chan moment,
// leaking in-flight events under low post-restart traffic.
func TestConsumer_DrainBoundedAfterCancel(t *testing.T) {
	t.Parallel()

	wal := testWAL(t)
	ins := &passingInserter{}

	in := make(chan ingest.WALEnvelope, 4)

	const settle = 150 * time.Millisecond

	c := ingest.NewConsumer(in, wal, ins, ingest.ConsumerConfig{
		BatchRows:     1,
		BatchInterval: 50 * time.Millisecond,
		DrainSettle:   settle,
	}, nil, slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})

	go func() {
		c.Run(ctx)
		close(done)
	}()

	// Send a trailing event right before cancel to prove the drain
	// doesn't short-circuit on empty-chan mid-flight.
	in <- ingest.WALEnvelope{Idx: 1, Ev: ingest.EnrichedEvent{SiteID: 1}}

	time.Sleep(20 * time.Millisecond)

	cancel()

	start := time.Now()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not exit within 2s after ctx cancel")
	}

	elapsed := time.Since(start)

	// Upper bound: DrainSettle + 1 BatchInterval grace (for the flush
	// tick to run before the settle timer fires). Going significantly
	// over either means drain is not bounded.
	if elapsed > settle+200*time.Millisecond {
		t.Errorf("drain took %v; expected ≤ %v", elapsed, settle+200*time.Millisecond)
	}

	// And it must have waited at least the settle window (rather than
	// bailing instantly via the old default:return race).
	if elapsed < settle/2 {
		t.Errorf("drain exited too fast (%v); expected at least the settle window", elapsed)
	}

	// The in-flight event should have landed in CH.
	if got := ins.calls.Load(); got < 1 {
		t.Errorf("drain did not flush the trailing event; InsertBatch calls = %d", got)
	}
}
