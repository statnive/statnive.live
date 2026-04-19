package ingest_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/ingest"
)

func TestBurstGuard_AllowsUnderCap(t *testing.T) {
	t.Parallel()

	g := ingest.NewBurstGuard(500)
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	v := [16]byte{1}

	for i := 0; i < 500; i++ {
		if !g.Allow(v, now) {
			t.Fatalf("event %d unexpectedly rejected", i)
		}
	}
}

func TestBurstGuard_RejectsAboveCap(t *testing.T) {
	t.Parallel()

	g := ingest.NewBurstGuard(500)
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	v := [16]byte{2}

	allowed := 0
	rejected := 0

	for i := 0; i < 1000; i++ {
		if g.Allow(v, now) {
			allowed++
		} else {
			rejected++
		}
	}

	if allowed != 500 || rejected != 500 {
		t.Errorf("allowed=%d rejected=%d, want 500/500", allowed, rejected)
	}
}

func TestBurstGuard_WindowResets(t *testing.T) {
	t.Parallel()

	g := ingest.NewBurstGuard(10)
	t0 := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	v := [16]byte{3}

	// Burn the budget.
	for i := 0; i < 10; i++ {
		g.Allow(v, t0)
	}
	if g.Allow(v, t0) {
		t.Fatal("11th event should be rejected within the window")
	}

	// 60s later — window should reset.
	t1 := t0.Add(time.Minute + time.Second)
	if !g.Allow(v, t1) {
		t.Errorf("event after window expiry should be allowed")
	}
}

func TestBurstGuard_PerVisitor(t *testing.T) {
	t.Parallel()

	g := ingest.NewBurstGuard(5)
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	// 1000 distinct visitors each fire 1 event — all allowed (cap is per-visitor).
	for i := 0; i < 1000; i++ {
		v := [16]byte{}
		v[0] = byte(i)
		v[1] = byte(i >> 8)

		if !g.Allow(v, now) {
			t.Fatalf("visitor %d unexpectedly rejected", i)
		}
	}
}

func TestBurstGuard_ZeroCapDisablesGuard(t *testing.T) {
	t.Parallel()

	g := ingest.NewBurstGuard(0)
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	v := [16]byte{4}

	for i := 0; i < 10000; i++ {
		if !g.Allow(v, now) {
			t.Fatalf("event %d rejected with cap=0", i)
		}
	}
}

func TestBurstGuard_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	g := ingest.NewBurstGuard(100)
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	const (
		workers       = 50
		eventsPerWorker = 1000
	)

	var (
		allowed atomic.Int64
		wg      sync.WaitGroup
	)

	for w := 0; w < workers; w++ {
		wg.Add(1)

		go func(id int) {
			defer wg.Done()

			v := [16]byte{}
			v[0] = byte(id)

			for i := 0; i < eventsPerWorker; i++ {
				if g.Allow(v, now) {
					allowed.Add(1)
				}
			}
		}(w)
	}

	wg.Wait()

	// Each worker has its own visitor → each gets exactly cap=100 allowed.
	want := int64(workers * 100)
	if got := allowed.Load(); got != want {
		t.Errorf("concurrent allowed = %d, want %d", got, want)
	}
}

func TestBurstGuard_ActiveVisitorsCount(t *testing.T) {
	t.Parallel()

	g := ingest.NewBurstGuard(500)
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 100; i++ {
		v := [16]byte{}
		v[0] = byte(i)
		v[1] = byte(i >> 8)
		g.Allow(v, now)
	}

	if got := g.ActiveVisitors(); got != 100 {
		t.Errorf("ActiveVisitors = %d, want 100", got)
	}
}
