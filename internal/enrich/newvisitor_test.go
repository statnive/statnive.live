package enrich_test

import (
	"path/filepath"
	"testing"

	"github.com/statnive/statnive.live/internal/enrich"
)

func TestNewVisitorFilter_FirstSeenIsNew(t *testing.T) {
	t.Parallel()

	f := enrich.NewNewVisitorFilter(1024, 0.001)

	hash := [16]byte{1}
	if !f.CheckAndMark(hash, hash) {
		t.Error("first sighting should be new")
	}

	if f.CheckAndMark(hash, hash) {
		t.Error("second sighting should be returning")
	}
}

func TestNewVisitorFilter_CrossDayGrace(t *testing.T) {
	t.Parallel()

	f := enrich.NewNewVisitorFilter(1024, 0.001)

	yesterday := [16]byte{0xaa}
	today := [16]byte{0xbb}

	// Visitor seen yesterday under salt(yesterday).
	if !f.CheckAndMark(yesterday, yesterday) {
		t.Error("first sighting should be new")
	}

	// Returns at 00:02 IRST: today's hash differs (new salt) but bloom
	// remembers yesterday's hash. CheckAndMark must NOT flag as new.
	if f.CheckAndMark(today, yesterday) {
		t.Error("cross-day-grace lookup should classify as returning")
	}

	// Now the visitor's third event in the same session: today's salt
	// is the only valid one (we passed equal hashes for today). The
	// previous CheckAndMark added today's hash to the bloom for warmup,
	// so this must be returning (cheap path).
	if f.CheckAndMark(today, today) {
		t.Error("post-grace event should be returning")
	}
}

func TestNewVisitorFilter_PersistRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "bloom.dat")

	a := enrich.NewNewVisitorFilter(1024, 0.001)

	for i := byte(0); i < 50; i++ {
		h := [16]byte{i}
		a.CheckAndMark(h, h)
	}

	if err := a.SaveTo(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	b := enrich.NewNewVisitorFilter(1024, 0.001)
	if err := b.LoadFrom(path); err != nil {
		t.Fatalf("load: %v", err)
	}

	for i := byte(0); i < 50; i++ {
		h := [16]byte{i}
		if b.CheckAndMark(h, h) {
			t.Errorf("hash %d should be remembered after reload", i)
		}
	}
}

func TestNewVisitorFilter_LoadFromMissingIsNoop(t *testing.T) {
	t.Parallel()

	f := enrich.NewNewVisitorFilter(1024, 0.001)
	if err := f.LoadFrom(filepath.Join(t.TempDir(), "nope.dat")); err != nil {
		t.Errorf("missing file should be a no-op, got: %v", err)
	}
}
