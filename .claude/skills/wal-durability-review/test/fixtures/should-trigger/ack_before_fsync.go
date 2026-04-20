// Should-trigger fixture for wal-durability-review:ack-before-fsync.
//
// The handler writes the WAL entry then immediately responds 202 with
// no Sync barrier in between. kill -9 between the response and the
// next periodic fsync loses up to walFsyncInterval of events.
package fixtures

import "net/http"

type wal struct{}

func (*wal) Write(idx uint64, data []byte) error { return nil }
func (*wal) Sync() error                         { return nil }

type handler struct {
	wal *wal
}

func (h *handler) ingest(w http.ResponseWriter, _ *http.Request) {
	// ruleid: ack-before-fsync
	_ = h.wal.Write(1, []byte("event"))
	w.WriteHeader(http.StatusAccepted)
}