package ingest

import (
	"context"
	"log/slog"
	"time"
)

// ConsumerConfig caps the dual-trigger batcher. PLAN.md:158, doc 24 §Sec 1.5:
// flush on rows OR interval OR bytes — first to fire wins.
type ConsumerConfig struct {
	BatchRows     int
	BatchInterval time.Duration
	BatchMaxBytes int
}

// Inserter is the abstraction the consumer needs from the storage layer.
// Mockable in unit tests; satisfied by *storage.ClickHouseStore.
type Inserter interface {
	InsertBatch(ctx context.Context, events []EnrichedEvent) error
}

// Consumer drains the enriched-event channel, persists each event to the WAL
// for durability, batches rows, and flushes to the Inserter on the first
// trigger to fire. After every successful flush it ack's the WAL so segments
// can be reclaimed.
type Consumer struct {
	in     <-chan EnrichedEvent
	wal    *WALWriter
	store  Inserter
	cfg    ConsumerConfig
	logger *slog.Logger
}

// NewConsumer wires the consumer. cfg defaults: 1000 rows / 500 ms / 10 MB.
func NewConsumer(in <-chan EnrichedEvent, wal *WALWriter, store Inserter, cfg ConsumerConfig, logger *slog.Logger) *Consumer {
	if cfg.BatchRows <= 0 {
		cfg.BatchRows = 1000
	}

	if cfg.BatchInterval <= 0 {
		cfg.BatchInterval = 500 * time.Millisecond
	}

	if cfg.BatchMaxBytes <= 0 {
		cfg.BatchMaxBytes = 10 * 1024 * 1024
	}

	return &Consumer{in: in, wal: wal, store: store, cfg: cfg, logger: logger}
}

// Run blocks until ctx is canceled or the in channel closes.
func (c *Consumer) Run(ctx context.Context) {
	batch := make([]EnrichedEvent, 0, c.cfg.BatchRows)

	var (
		lastWALIdx uint64
		batchBytes int
	)

	ticker := time.NewTicker(c.cfg.BatchInterval)
	defer ticker.Stop()

	flush := func(reason string) {
		if len(batch) == 0 {
			return
		}

		fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := c.store.InsertBatch(fctx, batch)

		cancel()

		if err != nil {
			// Ack the WAL anyway so we don't replay forever (PLAN.md:158
			// — "no log.Panicf on retry exhaust"). Real DLQ ships in Phase 2.
			c.logger.Error("flush failed after retry", "reason", reason, "err", err, "rows", len(batch))
		} else {
			c.logger.Debug("flush ok", "reason", reason, "rows", len(batch))
		}

		if lastWALIdx > 0 {
			if ackErr := c.wal.Ack(lastWALIdx); ackErr != nil {
				c.logger.Warn("wal ack failed", "err", ackErr)
			}
		}

		batch = batch[:0]
		batchBytes = 0
	}

	add := func(ev EnrichedEvent) bool {
		if walErr := c.wal.Append(ev); walErr != nil {
			c.logger.Warn("wal append", "err", walErr)
		}

		lastWALIdx = c.wal.CurrentIndex()
		batch = append(batch, ev)
		batchBytes += approxRowBytes(&ev)

		return len(batch) >= c.cfg.BatchRows || batchBytes >= c.cfg.BatchMaxBytes
	}

	for {
		select {
		case <-ctx.Done():
			c.drain(flush, add)

			return

		case ev, ok := <-c.in:
			if !ok {
				flush("channel-closed")
				_ = c.wal.Sync()

				return
			}

			if add(ev) {
				flush("size")
				ticker.Reset(c.cfg.BatchInterval)
			}

		case <-ticker.C:
			flush("timeout")
		}
	}
}

func (c *Consumer) drain(flush func(reason string), add func(EnrichedEvent) bool) {
	for {
		select {
		case ev, ok := <-c.in:
			if !ok {
				flush("shutdown-drain")
				_ = c.wal.Sync()

				return
			}

			if add(ev) {
				flush("shutdown-size")
			}
		default:
			flush("shutdown")
			_ = c.wal.Sync()

			return
		}
	}
}

// approxRowBytes is a cheap upper-bound estimate used for the bytes trigger.
// We don't need exact serialized size — gob-encoding to measure would be
// wasted CPU on the hot path.
func approxRowBytes(e *EnrichedEvent) int {
	const fixedOverhead = 256 // headers, fixed-width fields

	total := fixedOverhead +
		len(e.UserIDHash) +
		len(e.CookieID) +
		len(e.Hostname) +
		len(e.Pathname) +
		len(e.Title) +
		len(e.Referrer) +
		len(e.UTMCampaign) +
		len(e.UTMContent) +
		len(e.UTMTerm) +
		len(e.UserSegment)

	for _, k := range e.PropKeys {
		total += len(k)
	}

	for _, v := range e.PropVals {
		total += len(v)
	}

	return total
}
