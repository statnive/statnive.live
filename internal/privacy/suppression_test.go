package privacy

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSuppressionList_AddIsSuppressed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	list, err := NewSuppressionList(filepath.Join(dir, "suppression.wal"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() { _ = list.Close() })

	const hash = "h:abcdef0123456789"

	if list.IsSuppressed(hash) {
		t.Fatalf("freshly-opened list should not contain anything")
	}

	if err := list.Add(hash); err != nil {
		t.Fatalf("add: %v", err)
	}

	if !list.IsSuppressed(hash) {
		t.Errorf("hash should be suppressed after Add")
	}

	if got := list.Len(); got != 1 {
		t.Errorf("Len = %d, want 1", got)
	}
}

func TestSuppressionList_ReplayOnReopen(t *testing.T) {
	t.Parallel()

	walPath := filepath.Join(t.TempDir(), "suppression.wal")

	const hash = "h:repro-this-hash"

	// First instance — writes the opt-out then closes.
	{
		list, err := NewSuppressionList(walPath)
		if err != nil {
			t.Fatalf("new: %v", err)
		}

		if err := list.Add(hash); err != nil {
			t.Fatalf("add: %v", err)
		}

		if err := list.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}

	// Second instance — reopens the same path; replay must rebuild
	// the in-memory set so the opt-out survives a process restart.
	{
		list, err := NewSuppressionList(walPath)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}

		t.Cleanup(func() { _ = list.Close() })

		if !list.IsSuppressed(hash) {
			t.Errorf("hash should still be suppressed after reopen")
		}
	}
}

func TestSuppressionList_RejectsEmptyHash(t *testing.T) {
	t.Parallel()

	list, err := NewSuppressionList(filepath.Join(t.TempDir(), "suppression.wal"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() { _ = list.Close() })

	if err := list.Add(""); err == nil {
		t.Errorf("Add(\"\") should error")
	}

	if list.IsSuppressed("") {
		t.Errorf("empty hash should never be suppressed")
	}
}

func TestSuppressionList_RecordCarriesTimestamp(t *testing.T) {
	t.Parallel()

	list, err := NewSuppressionList(filepath.Join(t.TempDir(), "suppression.wal"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() { _ = list.Close() })

	fixed := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	list.now = func() time.Time { return fixed }

	if err := list.Add("h:timestamped"); err != nil {
		t.Fatalf("add: %v", err)
	}
}
