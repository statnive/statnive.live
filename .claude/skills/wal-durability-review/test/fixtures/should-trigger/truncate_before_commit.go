// Should-trigger fixture for wal-durability-review:truncate-before-commit.
//
// Truncate-then-commit is the WAL durability anti-pattern: if the CH
// insert fails, the batch is gone from the WAL. Order MUST be
// try-insert → on-success-truncate.
package fixtures

import "context"

type wal2 struct{}

func (*wal2) TruncateFront(idx uint64) error { return nil }

type store struct{}

func (*store) InsertBatch(ctx context.Context, rows []any) error { return nil }

type consumer struct {
	wal   *wal2
	store *store
}

func (c *consumer) badFlush(ctx context.Context, lastIdx uint64, batch []any) error {
	// ruleid: truncate-before-commit
	_ = c.wal.TruncateFront(lastIdx + 1)

	return c.store.InsertBatch(ctx, batch)
}