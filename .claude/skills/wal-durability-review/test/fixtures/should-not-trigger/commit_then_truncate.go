// Should-not-trigger fixture for truncate-before-commit.
//
// InsertBatch runs FIRST; TruncateFront only fires on success. On CH
// failure the WAL stays intact and the next flush retries. Matches
// consumer.go:insertWithRetry + flush post-7b1b.
package fixtures

import "context"

type walOK2 struct{}

func (*walOK2) TruncateFront(idx uint64) error { return nil }

type storeOK struct{}

func (*storeOK) InsertBatch(ctx context.Context, rows []any) error { return nil }

type consumerOK struct {
	wal   *walOK2
	store *storeOK
}

func (c *consumerOK) goodFlush(ctx context.Context, lastIdx uint64, batch []any) error {
	// ok: truncate-before-commit
	if err := c.store.InsertBatch(ctx, batch); err != nil {
		return err
	}

	return c.wal.TruncateFront(lastIdx + 1)
}
