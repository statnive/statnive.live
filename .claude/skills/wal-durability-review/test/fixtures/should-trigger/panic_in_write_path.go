// Should-trigger fixture for wal-durability-review:panic-in-write-path.
//
// A bare panic() inside Append crashes the binary on every event.
// Reserve panic / os.Exit for the Sync error path (item #2).
package fixtures

type wal3 struct{}

func (*wal3) writeLow(idx uint64, data []byte) error { return nil }

func (w *wal3) Append(idx uint64, data []byte) error {
	if err := w.writeLow(idx, data); err != nil {
		// ruleid: panic-in-write-path
		panic("disk write failed")
	}

	return nil
}