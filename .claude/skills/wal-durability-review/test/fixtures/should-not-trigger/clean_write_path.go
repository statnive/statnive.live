// Should-not-trigger fixture for panic-in-write-path.
//
// Append returns errors to the caller. The only place process
// termination is allowed is the Sync error path (item #2) — and even
// there we use os.Exit, not panic, so defers don't swallow the EIO.
package fixtures

type walOK3 struct{}

func (*walOK3) writeLow(idx uint64, data []byte) error { return nil }

func (w *walOK3) Append(idx uint64, data []byte) error {
	// ok: panic-in-write-path
	if err := w.writeLow(idx, data); err != nil {
		return err
	}

	return nil
}
