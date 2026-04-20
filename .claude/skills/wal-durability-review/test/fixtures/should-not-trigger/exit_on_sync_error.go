// Should-not-trigger fixture for sync-error-swallowed.
//
// Sync error → os.Exit(1). Orchestrator restarts; restart re-opens
// the WAL fresh (fsyncgate 2018, LWN 752063). This is the correct
// doc 27 §Gap 1 item #2 pattern — never log-and-continue.
package fixtures

import (
	"log/slog"
	"os"
)

type logOK struct{}

func (*logOK) Sync() error { return nil }

func goodSync(l *logOK, slogger *slog.Logger) {
	// ok: sync-error-swallowed
	if err := l.Sync(); err != nil {
		slogger.Error("wal sync failed, exiting", "err", err)
		os.Exit(1)
	}
}
