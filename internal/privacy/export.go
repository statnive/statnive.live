package privacy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

var (
	errExportEmptyHash   = errors.New("export: empty cookie hash")
	errExportEmptySiteID = errors.New("export: site_id is 0")
)

// defaultExportMaxRows caps the per-request row count. A single
// visitor with more than 10K rows indicates abuse or a misbehaving
// SPA; the response sets `truncated: true` so an operator audit can
// catch the case.
const defaultExportMaxRows = 10000

// VisitorExporter reads a visitor's events_raw rows for DSAR Art. 15 /
// Art. 20 fulfilment. The query is scoped by BOTH cookie_id AND
// site_id — same cross-tenant filter as EraseEnumerator (see
// erase.go:buildEraseSQL) — so a visitor on site A never sees site B's
// rows even if the cookie hash collides. Export covers events_raw
// only; rollup tables aren't queried because they hold aggregates
// rather than individual rows (Art. 15 is about the latter). If a
// future migration adds a second cookie_id-bearing base table (next
// to events_raw), extend the SELECT below — erase.go discovers tables
// dynamically and will pick it up automatically, so the surface
// returned here would otherwise silently under-disclose what erase
// would delete.
type VisitorExporter struct {
	conn     driver.Conn
	database string
	maxRows  int
}

// NewVisitorExporter constructs the exporter. database is the
// ClickHouse schema (typically "statnive").
func NewVisitorExporter(conn driver.Conn, database string) *VisitorExporter {
	return &VisitorExporter{
		conn:     conn,
		database: database,
		maxRows:  defaultExportMaxRows,
	}
}

// ExportRow is one events_raw row, redacted to columns the visitor
// supplied or that describe their session. Server-internal columns
// (visitor_hash, user_id_hash, user_segment, is_bot) are excluded —
// they aren't meaningful to the visitor and disclosing them would leak
// operator metadata. cookie_id is also excluded; the response envelope
// surfaces it once as cookie_id_hash.
type ExportRow struct {
	Time          time.Time `json:"time"`
	Hostname      string    `json:"hostname"`
	Pathname      string    `json:"pathname"`
	Title         string    `json:"title"`
	Referrer      string    `json:"referrer"`
	ReferrerName  string    `json:"referrer_name"`
	Channel       string    `json:"channel"`
	UTMSource     string    `json:"utm_source"`
	UTMMedium     string    `json:"utm_medium"`
	UTMCampaign   string    `json:"utm_campaign"`
	UTMContent    string    `json:"utm_content"`
	UTMTerm       string    `json:"utm_term"`
	Province      string    `json:"province"`
	City          string    `json:"city"`
	CountryCode   string    `json:"country_code"`
	ISP           string    `json:"isp"`
	Carrier       string    `json:"carrier"`
	OS            string    `json:"os"`
	Browser       string    `json:"browser"`
	DeviceType    string    `json:"device_type"`
	ViewportWidth uint16    `json:"viewport_width"`
	EventType     string    `json:"event_type"`
	EventName     string    `json:"event_name"`
	EventValue    uint64    `json:"event_value"`
	IsGoal        uint8     `json:"is_goal"`
	IsNew         uint8     `json:"is_new"`
	PropKeys      []string  `json:"prop_keys"`
	PropVals      []string  `json:"prop_vals"`
}

// ExportResult is the JSON envelope returned to the visitor.
type ExportResult struct {
	SiteID       uint32      `json:"site_id"`
	CookieIDHash string      `json:"cookie_id_hash"`
	RowCount     int         `json:"row_count"`
	Truncated    bool        `json:"truncated"`
	GeneratedAt  time.Time   `json:"generated_at"`
	Rows         []ExportRow `json:"rows"`
}

// ExportVisitorRows returns the visitor's events_raw rows for the
// requesting site_id, ordered by time DESC, capped at maxRows. Empty
// cookieIDHash or siteID=0 is rejected so a caller cannot accidentally
// scan the cross-tenant join — same guard as EraseByCookieID.
func (e *VisitorExporter) ExportVisitorRows(
	ctx context.Context,
	siteID uint32,
	cookieIDHash string,
) (ExportResult, error) {
	if cookieIDHash == "" {
		return ExportResult{}, errExportEmptyHash
	}

	if siteID == 0 {
		return ExportResult{}, errExportEmptySiteID
	}

	// LIMIT maxRows+1 so a single round-trip tells us whether the
	// result was truncated.
	rows, err := e.conn.Query(ctx, buildExportSQL(e.database), cookieIDHash, siteID, e.maxRows+1)
	if err != nil {
		return ExportResult{}, fmt.Errorf("export: query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := ExportResult{
		SiteID:       siteID,
		CookieIDHash: cookieIDHash,
		Rows:         make([]ExportRow, 0, e.maxRows),
		GeneratedAt:  time.Now().UTC(),
	}

	for rows.Next() {
		if len(out.Rows) == e.maxRows {
			out.Truncated = true

			break
		}

		var r ExportRow

		if scanErr := rows.Scan(
			&r.Time, &r.Hostname, &r.Pathname, &r.Title,
			&r.Referrer, &r.ReferrerName, &r.Channel,
			&r.UTMSource, &r.UTMMedium, &r.UTMCampaign, &r.UTMContent, &r.UTMTerm,
			&r.Province, &r.City, &r.CountryCode,
			&r.ISP, &r.Carrier,
			&r.OS, &r.Browser, &r.DeviceType, &r.ViewportWidth,
			&r.EventType, &r.EventName, &r.EventValue, &r.IsGoal, &r.IsNew,
			&r.PropKeys, &r.PropVals,
		); scanErr != nil {
			return ExportResult{}, fmt.Errorf("export: scan: %w", scanErr)
		}

		out.Rows = append(out.Rows, r)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return ExportResult{}, fmt.Errorf("export: rows: %w", rowsErr)
	}

	out.RowCount = len(out.Rows)

	return out, nil
}

// buildExportSQL produces the events_raw SELECT for one visitor on one
// site. The `cookie_id = ? AND site_id = ?` filter mirrors
// erase.go:buildEraseSQL — keep the two in sync so Art. 15 surfaces
// exactly the rows Art. 17 would delete. Bind order:
// cookieIDHash, siteID, limit. SETTINGS line matches the dashboard
// raw-fallback convention (queries.go:371) — clickhouse-operations-
// review skill rule 4 (every customer SELECT carries
// max_execution_time + max_memory_usage caps).
func buildExportSQL(database string) string {
	return fmt.Sprintf(`
		SELECT
			time, hostname, pathname, title,
			referrer, referrer_name, channel,
			utm_source, utm_medium, utm_campaign, utm_content, utm_term,
			province, city, country_code,
			isp, carrier,
			os, browser, device_type, viewport_width,
			event_type, event_name, event_value, is_goal, is_new,
			prop_keys, prop_vals
		FROM %s.events_raw
		WHERE cookie_id = ? AND site_id = ?
		ORDER BY time DESC
		LIMIT ?
		SETTINGS max_execution_time = 30, max_memory_usage = 8589934592
	`, quoteIdent(database))
}
