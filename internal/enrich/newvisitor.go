package enrich

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/bits-and-blooms/bloom/v3"
)

// NewVisitorFilter is the bloom filter used to flag is_new on every event.
//
// Sized at 10M entries / 0.001 false-positive rate per PLAN.md feature #38
// (≈18 MB resident). FP rate of 0.001 means ~1 in 1000 returning visitors
// is misclassified as new — well within the analytics-invariant budget for
// "new vs returning" accuracy at SamplePlatform's 10–20M DAU scale.
//
// Cross-day grace (PLAN.md verification 22, doc 24 §Sec 1.1):
// CheckAndMark accepts both today's and yesterday's hashes for the same
// visitor. Only when neither is present do we flag as new and add today's.
// This closes the "user enters site at 23:59 IRST, returns at 00:02"
// ghost-session bug without a second bloom or any persistent state.
type NewVisitorFilter struct {
	mu sync.Mutex
	bf *bloom.BloomFilter
}

// NewNewVisitorFilter constructs a sized filter. PLAN defaults: cap=10M, fp=0.001.
func NewNewVisitorFilter(capacity uint, fpRate float64) *NewVisitorFilter {
	return &NewVisitorFilter{bf: bloom.NewWithEstimates(capacity, fpRate)}
}

// CheckAndMark reports whether the visitor is new and atomically marks the
// current hash as seen. It tries currentHash first, then prevHash (only
// useful around the IRST midnight boundary). When prevHash equals
// currentHash (most of the day), the second probe is a harmless duplicate.
//
// Returns true ⇔ both probes missed.
func (n *NewVisitorFilter) CheckAndMark(currentHash, prevHash [16]byte) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.bf.Test(currentHash[:]) {
		return false
	}

	if currentHash != prevHash && n.bf.Test(prevHash[:]) {
		// Returning visitor crossing midnight — bring them forward into
		// today's bloom so the next event hits the cheap path.
		n.bf.Add(currentHash[:])

		return false
	}

	n.bf.Add(currentHash[:])

	return true
}

// LoadFrom reads a previously persisted filter from disk. Returns nil + a
// fresh-filter zero state when the file doesn't exist (first-run case).
func (n *NewVisitorFilter) LoadFrom(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("bloom open: %w", err)
	}
	defer func() { _ = f.Close() }()

	n.mu.Lock()
	defer n.mu.Unlock()

	if _, readErr := n.bf.ReadFrom(f); readErr != nil {
		return fmt.Errorf("bloom read: %w", readErr)
	}

	return nil
}

// SaveTo writes the filter to disk atomically (temp + rename) so a crash
// mid-write doesn't leave a half-serialized file the next boot can't load.
func (n *NewVisitorFilter) SaveTo(path string) error {
	tmp := path + ".tmp"

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("bloom create: %w", err)
	}

	n.mu.Lock()
	_, writeErr := n.bf.WriteTo(f)
	n.mu.Unlock()

	if syncErr := f.Sync(); syncErr != nil && writeErr == nil {
		writeErr = syncErr
	}

	if closeErr := f.Close(); closeErr != nil && writeErr == nil {
		writeErr = closeErr
	}

	if writeErr != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("bloom write: %w", writeErr)
	}

	if renameErr := os.Rename(tmp, path); renameErr != nil {
		return fmt.Errorf("bloom rename: %w", renameErr)
	}

	return nil
}

// EstimatedCount returns the bloom's approximate entry count. Used by
// /healthz to surface "bloom warmth" after a restart.
func (n *NewVisitorFilter) EstimatedCount() uint32 {
	n.mu.Lock()
	defer n.mu.Unlock()

	return n.bf.ApproximatedSize()
}
