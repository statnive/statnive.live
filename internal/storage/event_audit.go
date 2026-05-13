package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	errEventAuditZeroSiteID    = errors.New("event_audit: site_id is zero")
	errEventAuditWindowInverse = errors.New("event_audit: from must be before to")
)

// EventNameCount is one row of the event-name cardinality audit.
// Returned by EventNameCardinality and consumed by the admin
// /api/admin/event-audit handler.
type EventNameCount struct {
	Name  string `json:"name"`
	Count uint64 `json:"count"`
}

// EventNameCardinality returns the distinct event_name values fired by
// a site within [from, to), with per-name counts, sorted descending.
// Reads events_raw — the second documented exception to Architecture
// Rule 1 (the first being funnels via windowFunnel). Tenancy choke
// point is preserved by leading the WHERE with `site_id = ?`.
//
// The window is bounded by the caller; callers SHOULD pass a window
// that fits inside the events_raw 180-day TTL.
func (s *ClickHouseStore) EventNameCardinality(
	ctx context.Context,
	siteID uint32,
	from, to time.Time,
) ([]EventNameCount, error) {
	if siteID == 0 {
		return nil, errEventAuditZeroSiteID
	}

	if !from.Before(to) {
		return nil, errEventAuditWindowInverse
	}

	rows, err := s.conn.Query(ctx, `
		SELECT event_name, toUInt64(count()) AS n
		FROM statnive.events_raw
		WHERE site_id = ?
		  AND time >= ?
		  AND time < ?
		  AND is_bot = 0
		GROUP BY event_name
		ORDER BY n DESC
		SETTINGS max_execution_time = 10
	`, siteID, from, to)
	if err != nil {
		return nil, fmt.Errorf("event_audit query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := make([]EventNameCount, 0, 8)

	for rows.Next() {
		var (
			name  string
			count uint64
		)

		if scanErr := rows.Scan(&name, &count); scanErr != nil {
			return nil, fmt.Errorf("event_audit scan: %w", scanErr)
		}

		out = append(out, EventNameCount{Name: name, Count: count})
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("event_audit rows: %w", rowsErr)
	}

	return out, nil
}
