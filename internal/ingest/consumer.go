package ingest

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
)

// chInsertRetryDelays is the backoff schedule for transient ClickHouse
// errors (network blip, short restart). After the final delay the
// consumer gives up on the batch — the WAL stays intact, so the batch
// will retry on the next flush tick. Doc 27 §Gap 1 item 3: wal.Ack is
// gated on CH success; no ack on failure means no data loss, just
// back-pressure.
var chInsertRetryDelays = []time.Duration{
	100 * time.Millisecond,
	500 * time.Millisecond,
	2 * time.Second,
}

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

// Consumer drains the GroupSyncer's downstream channel and batches
// rows for ClickHouse. The WAL Append + Sync already happened in the
// GroupSyncer (handler-side); the consumer only needs to insert into
// CH and then ack the WAL. After every successful flush it ack's the
// WAL through the latest envelope's index so segments can be reclaimed.
//
// Phase 7b1b changed the input type from `<-chan EnrichedEvent` to
// `<-chan WALEnvelope` so the consumer can ack the right WAL index
// (rather than re-querying CurrentIndex inside its own goroutine, which
// would race with the GroupSyncer's writer).
type Consumer struct {
	in       <-chan WALEnvelope
	wal      *WALWriter
	store    Inserter
	cfg      ConsumerConfig
	auditLog *audit.Logger
	logger   *slog.Logger
}

// NewConsumer wires the consumer. cfg defaults: 1000 rows / 500 ms / 10 MB.
// auditLog is optional (nil silences audit emissions; test mode).
func NewConsumer(in <-chan WALEnvelope, wal *WALWriter, store Inserter, cfg ConsumerConfig, auditLog *audit.Logger, logger *slog.Logger) *Consumer {
	if cfg.BatchRows <= 0 {
		cfg.BatchRows = 1000
	}

	if cfg.BatchInterval <= 0 {
		cfg.BatchInterval = 500 * time.Millisecond
	}

	if cfg.BatchMaxBytes <= 0 {
		cfg.BatchMaxBytes = 10 * 1024 * 1024
	}

	return &Consumer{in: in, wal: wal, store: store, cfg: cfg, auditLog: auditLog, logger: logger}
}

// insertWithRetry attempts the CH insert with bounded backoff. Returns
// nil on success; the underlying error from the final attempt on
// exhaustion. Caller MUST gate wal.Ack on this returning nil
// (Architecture Rule 3 in wal-durability-review/SKILL.md).
func (c *Consumer) insertWithRetry(parent context.Context, batch []EnrichedEvent) error {
	var lastErr error

	attempts := len(chInsertRetryDelays) + 1

	for attempt := range attempts {
		fctx, cancel := context.WithTimeout(parent, 10*time.Second)
		err := c.store.InsertBatch(fctx, batch)

		cancel()

		if err == nil {
			return nil
		}

		lastErr = err

		if attempt == attempts-1 {
			break // out of retries
		}

		select {
		case <-time.After(chInsertRetryDelays[attempt]):
		case <-parent.Done():
			return errors.Join(lastErr, parent.Err())
		}
	}

	return lastErr
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

		err := c.insertWithRetry(ctx, batch)
		if err != nil {
			// CH unreachable beyond retry budget. DO NOT ack the WAL —
			// the events stay durable and the next flush retries from
			// the same WAL index. The fill_ratio backpressure middleware
			// will start returning 503 to handlers if the backlog grows
			// past 80% of the WAL cap (Architecture Rule 3 + item #6 in
			// wal-durability-review/SKILL.md).
			c.logger.Error("ch insert failed; batch retained in WAL",
				"reason", reason, "err", err, "rows", len(batch),
				"last_wal_idx", lastWALIdx)
			emitConsumerAudit(c.auditLog, audit.EventCHInsertFailed,
				slog.String("reason", reason),
				slog.String("err", err.Error()),
				slog.Int("rows", len(batch)),
				slog.Uint64("last_wal_idx", lastWALIdx))

			batch = batch[:0]
			batchBytes = 0

			return
		}

		c.logger.Debug("flush ok", "reason", reason, "rows", len(batch))

		if lastWALIdx > 0 {
			if ackErr := c.wal.Ack(lastWALIdx); ackErr != nil {
				c.logger.Warn("wal ack failed", "err", ackErr)
			}
		}

		batch = batch[:0]
		batchBytes = 0
	}

	// Events arrive already WAL-fsynced (GroupSyncer owns the Append
	// path). Consumer's job: batch and insert into CH, then ack the
	// WAL. No Append here — doing so would double-write.
	add := func(env WALEnvelope) bool {
		lastWALIdx = env.Idx
		batch = append(batch, env.Ev)
		batchBytes += approxRowBytes(&env.Ev)

		return len(batch) >= c.cfg.BatchRows || batchBytes >= c.cfg.BatchMaxBytes
	}

	for {
		select {
		case <-ctx.Done():
			c.drain(flush, add)

			return

		case env, ok := <-c.in:
			if !ok {
				flush("channel-closed")
				_ = c.wal.Sync()

				return
			}

			if add(env) {
				flush("size")
				ticker.Reset(c.cfg.BatchInterval)
			}

		case <-ticker.C:
			flush("timeout")
		}
	}
}

func (c *Consumer) drain(flush func(reason string), add func(WALEnvelope) bool) {
	for {
		select {
		case env, ok := <-c.in:
			if !ok {
				flush("shutdown-drain")
				_ = c.wal.Sync()

				return
			}

			if add(env) {
				flush("shutdown-size")
			}
		default:
			flush("shutdown")
			_ = c.wal.Sync()

			return
		}
	}
}

// emitConsumerAudit is a nil-safe wrapper around audit.Logger.Event so
// the consumer's audit emissions stay terse + the test path can pass
// auditLog: nil without an explicit guard at every call site. Mirrors
// emitAudit in handler.go.
func emitConsumerAudit(log *audit.Logger, name audit.EventName, attrs ...slog.Attr) {
	if log == nil {
		return
	}

	log.Event(context.Background(), name, attrs...)
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
