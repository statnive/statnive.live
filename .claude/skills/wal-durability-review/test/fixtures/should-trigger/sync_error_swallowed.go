// Should-trigger fixture for wal-durability-review:sync-error-swallowed.
//
// Pre-4.13 Linux fsync marks failed pages clean on EIO and forgets the
// error. Logging-and-continuing here means the next Sync() returns
// success but the data is gone (LWN 752063, fsyncgate 2018).
package fixtures

import "log/slog"

type log struct{}

func (*log) Sync() error { return nil }

func badSyncLoop(l *log, slogger *slog.Logger) {
	for {
		// ruleid: sync-error-swallowed
		if err := l.Sync(); err != nil {
			slogger.Warn("sync failed", "err", err)
		}
	}
}