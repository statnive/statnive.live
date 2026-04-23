package goals

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestMatch_HitAndMiss(t *testing.T) {
	t.Parallel()

	fs := newFakeStore()
	ctx := context.Background()

	_ = fs.Create(ctx, &Goal{
		SiteID: 1, Name: "Purchase", MatchType: MatchTypeEventNameEquals,
		Pattern: "purchase", ValueRials: 500_000, Enabled: true,
	})

	snap, err := NewSnapshot(ctx, fs)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}

	// Hit.
	if gID, val, ok := snap.Match(1, "purchase"); !ok || val != 500_000 || gID == uuid.Nil {
		t.Errorf("expected hit; got ok=%v val=%d id=%v", ok, val, gID)
	}

	// Miss — different event name.
	if _, _, ok := snap.Match(1, "checkout"); ok {
		t.Error("expected miss on different event name")
	}

	// Miss — site with no goals.
	if _, _, ok := snap.Match(999, "purchase"); ok {
		t.Error("expected miss on unknown site")
	}
}

// TestMatch_DoesNotCrossSite pins the tenancy isolation invariant:
// even with an identical event name, a goal configured on site_id=1
// must NOT match an event for site_id=2.
func TestMatch_DoesNotCrossSite(t *testing.T) {
	t.Parallel()

	fs := newFakeStore()
	ctx := context.Background()

	_ = fs.Create(ctx, &Goal{
		SiteID: 1, Name: "Site1-Purchase", MatchType: MatchTypeEventNameEquals,
		Pattern: "purchase", ValueRials: 500_000, Enabled: true,
	})

	snap, _ := NewSnapshot(ctx, fs)

	if _, _, ok := snap.Match(2, "purchase"); ok {
		t.Fatal("goal on site_id=1 leaked to site_id=2 — tenancy violation")
	}
}

func TestMatch_DisabledGoalSkipped(t *testing.T) {
	t.Parallel()

	fs := newFakeStore()
	ctx := context.Background()

	_ = fs.Create(ctx, &Goal{
		SiteID: 1, Name: "Disabled", MatchType: MatchTypeEventNameEquals,
		Pattern: "purchase", ValueRials: 500_000, Enabled: false,
	})

	snap, _ := NewSnapshot(ctx, fs)

	if _, _, ok := snap.Match(1, "purchase"); ok {
		t.Error("disabled goal matched — ListActive filter broken")
	}
}

func TestReload_AtomicSwap(t *testing.T) {
	t.Parallel()

	fs := newFakeStore()
	ctx := context.Background()

	_ = fs.Create(ctx, &Goal{
		SiteID: 1, Name: "A", MatchType: MatchTypeEventNameEquals,
		Pattern: "a", Enabled: true,
	})

	snap, _ := NewSnapshot(ctx, fs)

	if _, _, ok := snap.Match(1, "a"); !ok {
		t.Fatal("pre-reload miss")
	}

	_ = fs.Create(ctx, &Goal{
		SiteID: 1, Name: "B", MatchType: MatchTypeEventNameEquals,
		Pattern: "b", Enabled: true,
	})

	if _, _, ok := snap.Match(1, "b"); ok {
		t.Fatal("second goal matched before Reload — snapshot broken")
	}

	if err := snap.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if _, _, ok := snap.Match(1, "a"); !ok {
		t.Error("post-reload: original goal missing")
	}

	if _, _, ok := snap.Match(1, "b"); !ok {
		t.Error("post-reload: new goal missing")
	}
}

func TestReload_FailClosedKeepsPreviousSnapshot(t *testing.T) {
	t.Parallel()

	fs := newFakeStore()
	ctx := context.Background()

	_ = fs.Create(ctx, &Goal{
		SiteID: 1, Name: "A", MatchType: MatchTypeEventNameEquals,
		Pattern: "a", Enabled: true,
	})

	snap, _ := NewSnapshot(ctx, fs)

	if _, _, ok := snap.Match(1, "a"); !ok {
		t.Fatal("pre-fault miss")
	}

	// Inject a fault — ListActive errors.
	fs.listErr = errors.New("injected ListActive failure")

	if err := snap.Reload(ctx); err == nil {
		t.Fatal("Reload swallowed fault")
	}

	// Previous snapshot retained.
	if _, _, ok := snap.Match(1, "a"); !ok {
		t.Error("fault-closed: snapshot was lost")
	}
}

func TestSnapshot_SizeAndNilSafe(t *testing.T) {
	t.Parallel()

	var zero *Snapshot

	if _, _, ok := zero.Match(1, "x"); ok {
		t.Error("nil snapshot matched")
	}

	if zero.Size() != 0 {
		t.Error("nil snapshot size")
	}

	fs := newFakeStore()
	ctx := context.Background()

	_ = fs.Create(ctx, &Goal{SiteID: 1, Name: "A", MatchType: MatchTypeEventNameEquals, Pattern: "a", Enabled: true})
	_ = fs.Create(ctx, &Goal{SiteID: 2, Name: "B", MatchType: MatchTypeEventNameEquals, Pattern: "b", Enabled: true})

	snap, _ := NewSnapshot(ctx, fs)
	if snap.Size() != 2 {
		t.Errorf("Size = %d, want 2", snap.Size())
	}
}

func TestNopMatcher_NeverMatches(t *testing.T) {
	t.Parallel()

	if _, _, ok := (NopMatcher{}).Match(1, "purchase"); ok {
		t.Error("NopMatcher matched — contract violated")
	}
}

// TestGoalStep_P99Under200ns — bench-style regression test exercised
// via `make test`. Per doc 29 §1 the total enrich chain has a +10%
// budget; the goal step (one of seven stages) gets ~200 ns p99 per
// event as its proportional share. Stays off the bench harness
// because `make bench` doesn't ride CI (pre-impl review finding) —
// running it under `go test` means it regresses a PR, not a nightly.
func TestGoalStep_P99Under200ns(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping perf test under -short")
	}

	fs := newFakeStore()
	ctx := context.Background()

	// Seed a realistic goal count: 10 goals/site × 4 sites = 40 goals.
	for site := uint32(1); site <= 4; site++ {
		for i := range 10 {
			_ = fs.Create(ctx, &Goal{
				SiteID:    site,
				Name:      "goal",
				MatchType: MatchTypeEventNameEquals,
				Pattern:   patternFor(i),
				Enabled:   true,
			})
		}
	}

	snap, _ := NewSnapshot(ctx, fs)

	// Bench the typical miss path (most events don't match any goal).
	//nolint:thelper // the closure is the benchmark body itself, not a helper.
	res := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			snap.Match(1, "non-existent-event-name")
		}
	})

	nsPerOp := res.NsPerOp()
	// 200 ns per event is the p99 budget; testing.Benchmark returns
	// an average, so we allow 5x headroom vs. p99 to tolerate CI
	// noise while still catching an order-of-magnitude regression.
	if nsPerOp > 1000 {
		t.Errorf("Goal step: %d ns/op > 1000 ns — regression vs. 200 ns p99 budget", nsPerOp)
	}
}

func patternFor(i int) string {
	names := []string{
		"purchase", "signup", "subscribe", "download", "watch",
		"share", "comment", "upgrade", "checkout", "install",
	}

	return names[i%len(names)]
}
