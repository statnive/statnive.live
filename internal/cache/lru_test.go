package cache_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/cache"
)

func TestCache_GetSet(t *testing.T) {
	t.Parallel()

	c := cache.New(16)

	if _, ok := c.Get("missing"); ok {
		t.Error("Get on empty cache returned ok=true")
	}

	c.Set("k", 42, time.Minute)

	v, ok := c.Get("k")
	if !ok {
		t.Fatal("Get after Set returned ok=false")
	}

	if v.(int) != 42 {
		t.Errorf("v = %v, want 42", v)
	}
}

func TestCache_Wrap_OneLoaderCallPerKey(t *testing.T) {
	t.Parallel()

	c := cache.New(16)

	var calls atomic.Int32

	loader := func() (any, error) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond) // simulate query latency

		return "loaded", nil
	}

	const concurrent = 50

	var wg sync.WaitGroup
	for range concurrent {
		wg.Add(1)

		go func() {
			defer wg.Done()

			v, err := c.Wrap("hot-key", time.Minute, loader)
			if err != nil {
				t.Errorf("wrap: %v", err)
			}

			if v != "loaded" {
				t.Errorf("v = %v, want loaded", v)
			}
		}()
	}

	wg.Wait()

	// Loader is called at most once per key under concurrent miss thanks
	// to the per-key mutex — without it, all 50 goroutines would race to
	// hit ClickHouse at the same time.
	if got := calls.Load(); got != 1 {
		t.Errorf("loader called %d times, want exactly 1 (per-key mutex broken)", got)
	}
}

func TestCache_Wrap_PropagatesLoaderError(t *testing.T) {
	t.Parallel()

	c := cache.New(16)
	wantErr := errors.New("query failed")

	_, err := c.Wrap("k", time.Minute, func() (any, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}

	// Failed loads MUST NOT be cached — the next call must re-invoke
	// the loader (otherwise a transient ClickHouse blip poisons the
	// cache for its full TTL).
	var calls atomic.Int32

	_, _ = c.Wrap("k", time.Minute, func() (any, error) {
		calls.Add(1)

		return "ok", nil
	})

	if calls.Load() != 1 {
		t.Error("loader was not re-invoked after a previous failure")
	}
}

func TestCache_PerEntryTTLExpires(t *testing.T) {
	t.Parallel()

	c := cache.New(16)

	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	c.SetClock(func() time.Time { return now })

	c.Set("k", "v", 10*time.Second)

	if _, ok := c.Get("k"); !ok {
		t.Fatal("entry should be present immediately after Set")
	}

	// Advance clock past TTL.
	now = now.Add(11 * time.Second)

	if _, ok := c.Get("k"); ok {
		t.Error("entry should be expired after TTL elapsed")
	}
}

func TestCache_Wrap_HonorsPerCallTTL(t *testing.T) {
	t.Parallel()

	c := cache.New(16)

	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	c.SetClock(func() time.Time { return now })

	var calls int

	loader := func() (any, error) {
		calls++

		return calls, nil
	}

	// First call → miss → loader runs (returns 1).
	v, _ := c.Wrap("k", 5*time.Second, loader)
	if v.(int) != 1 || calls != 1 {
		t.Fatalf("first wrap: v=%v calls=%d", v, calls)
	}

	// Second call within TTL → hit → no loader.
	v, _ = c.Wrap("k", 5*time.Second, loader)
	if v.(int) != 1 || calls != 1 {
		t.Errorf("expected cache hit; v=%v calls=%d", v, calls)
	}

	// Advance past TTL → next call re-runs loader.
	now = now.Add(6 * time.Second)

	v, _ = c.Wrap("k", 5*time.Second, loader)
	if v.(int) != 2 || calls != 2 {
		t.Errorf("expected cache miss after TTL; v=%v calls=%d", v, calls)
	}
}

func TestCache_Purge(t *testing.T) {
	t.Parallel()

	c := cache.New(16)
	c.Set("a", 1, time.Minute)
	c.Set("b", 2, time.Minute)

	if c.Len() != 2 {
		t.Fatalf("Len before purge = %d, want 2", c.Len())
	}

	c.Purge()

	if c.Len() != 0 {
		t.Errorf("Len after purge = %d, want 0", c.Len())
	}
}
