package auth

import (
	"errors"
	"testing"
	"time"
)

func TestLockout_TripsAfterMaxFails(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cfg := LockoutConfig{
		MaxFails: 3,
		Decay:    time.Hour,
		Lockout:  10 * time.Minute,
		Now:      func() time.Time { return now },
	}
	lock := NewLockout(cfg)

	// First 3 fails are allowed.
	for i := 0; i < 3; i++ {
		if err := lock.Check("a@b.c"); err != nil {
			t.Fatalf("Check[%d]: %v", i, err)
		}

		lock.Record("a@b.c")
	}

	// 4th attempt — still inside the decay window, now locked.
	if err := lock.Check("a@b.c"); !errors.Is(err, ErrLockedOut) {
		t.Fatalf("after MaxFails: got %v, want ErrLockedOut", err)
	}

	// Different email — unaffected.
	if err := lock.Check("other@b.c"); err != nil {
		t.Fatalf("cross-email leak: %v", err)
	}
}

func TestLockout_ReleasesAfterWindow(t *testing.T) {
	ts := time.Unix(1_700_000_000, 0).UTC()
	cfg := LockoutConfig{
		MaxFails: 2,
		Decay:    time.Hour,
		Lockout:  time.Minute,
		Now:      func() time.Time { return ts },
	}
	lock := NewLockout(cfg)

	lock.Record("a@b.c")
	lock.Record("a@b.c")

	if err := lock.Check("a@b.c"); !errors.Is(err, ErrLockedOut) {
		t.Fatalf("expected lockout, got %v", err)
	}

	// Advance past the lockout window.
	ts = ts.Add(cfg.Lockout + time.Second)

	if err := lock.Check("a@b.c"); err != nil {
		t.Fatalf("after lockout window: %v", err)
	}
}

func TestLockout_ClearOnSuccess(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cfg := LockoutConfig{MaxFails: 10, Decay: time.Hour, Lockout: time.Minute, Now: func() time.Time { return now }}
	lock := NewLockout(cfg)

	lock.Record("a@b.c")
	lock.Record("a@b.c")
	lock.Clear("a@b.c")

	// Should be back to zero fail count.
	for i := 0; i < cfg.MaxFails-1; i++ {
		if err := lock.Check("a@b.c"); err != nil {
			t.Fatalf("after Clear, Check[%d]: %v", i, err)
		}

		lock.Record("a@b.c")
	}

	// MaxFails-th attempt is still allowed (not yet locked).
	if err := lock.Check("a@b.c"); err != nil {
		t.Fatalf("MaxFails-th attempt: %v", err)
	}
}

func TestLockout_CaseInsensitive(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cfg := LockoutConfig{MaxFails: 1, Decay: time.Hour, Lockout: time.Minute, Now: func() time.Time { return now }}
	lock := NewLockout(cfg)

	lock.Record("A@B.C")

	if err := lock.Check("a@b.c"); !errors.Is(err, ErrLockedOut) {
		t.Errorf("case mismatch leaked lockout: %v", err)
	}
}
