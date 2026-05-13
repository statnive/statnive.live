package privacy

import (
	"context"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// EraseEnumerator runs the visitor-erase mutation across every base
// MergeTree table that carries a cookie_id column. Materialized views
// are filtered out (engine LIKE '%MergeTree%' AND name NOT LIKE 'mv_%')
// — MVs auto-rebuild from their base table and reject direct UPDATEs.
// Erasing the base-table rows is what's authoritative; the MV's HLL
// state gets recomputed at next merge.
//
// dsar-completeness-checker skill enumerates system.tables dynamically
// so a new table added in a future migration WITHOUT a cookie_id
// column gets skipped, and one added WITH cookie_id is picked up
// without code changes. The discovery query is cached after first
// successful run; reset by a SIGHUP-style refresh in v1.1.
type EraseEnumerator struct {
	conn     driver.Conn
	database string
}

// NewEraseEnumerator constructs the enumerator. database is the
// ClickHouse schema name (typically "statnive").
func NewEraseEnumerator(conn driver.Conn, database string) *EraseEnumerator {
	return &EraseEnumerator{conn: conn, database: database}
}

// EraseResult reports the per-table outcome of one DSAR-erase call.
// Tables with no matching rows are still listed (DeletedRows: 0) so
// auditors can prove the mutation reached every store.
type EraseResult struct {
	Table        string `json:"table"`
	MutationSent bool   `json:"mutation_sent"`
	Err          string `json:"err,omitempty"`
}

// EraseByCookieID enumerates every MergeTree base table in the
// configured schema that has a cookie_id column, issues
// `ALTER TABLE ... DELETE WHERE cookie_id = ?` against each, and
// returns the per-table result. mutations_sync is NOT set — mutations
// run in the background; the caller acks 202 after WAL drain and the
// rows disappear at next merge.
//
// cookieIDHash MUST be the "h:" + hex form that hits events_raw post-
// Stage-1 PR #3. Empty hash is rejected (would match every visitor).
func (e *EraseEnumerator) EraseByCookieID(ctx context.Context, cookieIDHash string) ([]EraseResult, error) {
	if cookieIDHash == "" {
		return nil, fmt.Errorf("erase: empty cookie hash")
	}

	tables, err := e.discoverTablesWithCookieID(ctx)
	if err != nil {
		return nil, fmt.Errorf("erase: discover tables: %w", err)
	}

	out := make([]EraseResult, 0, len(tables))

	for _, table := range tables {
		result := EraseResult{Table: table}

		stmt := fmt.Sprintf(
			"ALTER TABLE %s.%s DELETE WHERE cookie_id = ?",
			quoteIdent(e.database), quoteIdent(table),
		)

		if execErr := e.conn.Exec(ctx, stmt, cookieIDHash); execErr != nil {
			result.Err = execErr.Error()
		} else {
			result.MutationSent = true
		}

		out = append(out, result)
	}

	return out, nil
}

// discoverTablesWithCookieID queries system.columns + system.tables to
// find every base MergeTree (or Replicated*MergeTree) in the schema
// that has a `cookie_id` column. Filters out materialized views and
// distributed engines — DELETE against either is wrong.
func (e *EraseEnumerator) discoverTablesWithCookieID(ctx context.Context) ([]string, error) {
	rows, err := e.conn.Query(ctx, `
		SELECT t.name
		FROM system.tables AS t
		INNER JOIN (
			SELECT DISTINCT table
			FROM system.columns
			WHERE database = ? AND name = 'cookie_id'
		) AS c ON c.table = t.name
		WHERE t.database = ?
		  AND t.engine LIKE '%MergeTree%'
		  AND t.engine NOT LIKE 'Distributed%'
		ORDER BY t.name
	`, e.database, e.database)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var tables []string

	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			return nil, scanErr
		}

		tables = append(tables, name)
	}

	return tables, rows.Err()
}

// quoteIdent wraps a ClickHouse identifier in backticks and escapes
// any interior backticks. Defence-in-depth — discovery should only
// surface system-validated names, but the caller controls
// e.database and we treat it as untrusted.
func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}
