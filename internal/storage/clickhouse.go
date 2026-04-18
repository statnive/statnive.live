// Package storage owns the ClickHouse connection, the 34-column batch insert
// against statnive.events_raw, and the schema migration runner.
//
// Dashboard query helpers (whereTimeAndTenant, Filter, Store interface) land
// in Phase 3 — see PLAN.md:188–190.
package storage

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/statnive/statnive.live/internal/ingest"
)

// 34-column INSERT — order MUST match ingest.EnrichedEvent field order
// (PLAN.md:160). site_id is column 1 to match the events_raw ORDER BY.
const insertStmt = `INSERT INTO statnive.events_raw (
	site_id, time, user_id_hash, cookie_id, visitor_hash, hostname, pathname,
	title, referrer, referrer_name, channel, utm_source, utm_medium,
	utm_campaign, utm_content, utm_term, province, city, country_code,
	isp, carrier, os, browser, device_type, viewport_width, event_type,
	event_name, event_value, is_goal, is_new, prop_keys, prop_vals,
	user_segment, is_bot
)`

// Config wires the ClickHouse driver. Audit-log path / DLQ are Phase 2.
type Config struct {
	Addrs    []string
	Database string
	Username string
	Password string
}

// ClickHouseStore is the batch-insert client. Hot-path counters are atomic
// so /healthz can read them without contending with the insert goroutine.
type ClickHouseStore struct {
	conn       driver.Conn
	logger     *slog.Logger
	lastInsert atomic.Int64 // unix nano
	rowsTotal  atomic.Uint64
}

// NewClickHouseStore opens a connection pool and verifies it with a Ping.
func NewClickHouseStore(ctx context.Context, cfg Config, logger *slog.Logger) (*ClickHouseStore, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: cfg.Addrs,
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		Compression:     &clickhouse.Compression{Method: clickhouse.CompressionLZ4},
		DialTimeout:     10 * time.Second,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
		Settings: clickhouse.Settings{
			"async_insert":          0,
			"wait_for_async_insert": 0,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("clickhouse open: %w", err)
	}

	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if pingErr := conn.Ping(pctx); pingErr != nil {
		_ = conn.Close()

		return nil, fmt.Errorf("clickhouse ping: %w", pingErr)
	}

	return &ClickHouseStore{conn: conn, logger: logger}, nil
}

// Conn exposes the raw driver to callers (e.g. sites.Registry) that need
// query-only access without opening a second pool.
func (s *ClickHouseStore) Conn() driver.Conn { return s.conn }

// Ping is the readiness probe surfaced by /healthz.
func (s *ClickHouseStore) Ping(ctx context.Context) error { return s.conn.Ping(ctx) }

// Close drains the pool. Safe to call multiple times.
func (s *ClickHouseStore) Close() error { return s.conn.Close() }

// LastInsert returns the wall-clock time of the most recent successful batch
// (zero if no batch has succeeded).
func (s *ClickHouseStore) LastInsert() time.Time {
	n := s.lastInsert.Load()
	if n == 0 {
		return time.Time{}
	}

	return time.Unix(0, n)
}

// RowsInserted is the cumulative count of rows persisted since process start.
func (s *ClickHouseStore) RowsInserted() uint64 { return s.rowsTotal.Load() }

// InsertBatch persists a slice of events with one retry on transient failure.
// On exhaustion it returns the wrapped error; the consumer logs + ack's the
// WAL anyway to avoid replay loops (PLAN.md:158 — "no log.Panicf on retry
// exhaust"). A real DLQ ships in Phase 2.
func (s *ClickHouseStore) InsertBatch(ctx context.Context, events []ingest.EnrichedEvent) error {
	if len(events) == 0 {
		return nil
	}

	if err := s.doInsert(ctx, events); err == nil {
		s.lastInsert.Store(time.Now().UnixNano())
		s.rowsTotal.Add(uint64(len(events)))

		return nil
	}

	// One retry — exponential backoff is overkill at this layer; the WAL
	// keeps the events durable across crashes.
	time.Sleep(250 * time.Millisecond)

	if err := s.doInsert(ctx, events); err != nil {
		s.logger.Error("clickhouse insert failed after retry",
			"err", err, "rows", len(events))

		return fmt.Errorf("insert after retry: %w", err)
	}

	s.lastInsert.Store(time.Now().UnixNano())
	s.rowsTotal.Add(uint64(len(events)))

	return nil
}

func (s *ClickHouseStore) doInsert(ctx context.Context, events []ingest.EnrichedEvent) error {
	batch, err := s.conn.PrepareBatch(ctx, insertStmt)
	if err != nil {
		return fmt.Errorf("prepare batch: %w", err)
	}

	defer func() {
		if !batch.IsSent() {
			_ = batch.Abort()
		}
	}()

	for i := range events {
		e := &events[i]
		if appendErr := batch.Append(
			e.SiteID,
			e.TSUTC,
			e.UserIDHash,
			e.CookieID,
			e.VisitorHash[:],
			e.Hostname,
			e.Pathname,
			e.Title,
			e.Referrer,
			e.ReferrerName,
			e.Channel,
			e.UTMSource,
			e.UTMMedium,
			e.UTMCampaign,
			e.UTMContent,
			e.UTMTerm,
			e.Province,
			e.City,
			e.CountryCode,
			e.ISP,
			e.Carrier,
			e.OS,
			e.Browser,
			e.DeviceType,
			e.ViewportWidth,
			e.EventType,
			e.EventName,
			e.EventValue,
			e.IsGoal,
			e.IsNew,
			e.PropKeys,
			e.PropVals,
			e.UserSegment,
			e.IsBot,
		); appendErr != nil {
			return fmt.Errorf("append row %d: %w", i, appendErr)
		}
	}

	if sendErr := batch.Send(); sendErr != nil {
		return fmt.Errorf("send batch: %w", sendErr)
	}

	return nil
}
