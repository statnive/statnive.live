// Should-not-trigger fixture for ack-before-fsync.
//
// The handler blocks on AppendAndWait — which internally calls the
// Write + Sync pair under a group-commit barrier — before writing
// 2xx. This is the correct doc 27 §Gap 1 pattern.
package fixtures

import (
	"context"
	"net/http"
)

type walOK struct{}

func (*walOK) AppendAndWait(ctx context.Context, data []byte) (uint64, error) {
	return 0, nil
}

type handlerOK struct {
	wal *walOK
}

func (h *handlerOK) ingest(w http.ResponseWriter, r *http.Request) {
	// ok: ack-before-fsync
	if _, err := h.wal.AppendAndWait(r.Context(), []byte("event")); err != nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)

		return
	}

	w.WriteHeader(http.StatusAccepted)
}
