package privacy

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

var (
	errEraseEmptyHash   = errors.New("erase: empty cookie hash")
	errEraseEmptySiteID = errors.New("erase: site_id is 0")
)

// completionPollInterval is how often WaitForCompletion polls
// system.mutations. 250ms balances audit-event latency (small enough
// that the typical sub-second mutation completes within one tick)
// against ClickHouse load (a single count() against system.mutations
// is microseconds).
const completionPollInterval = 250 * time.Millisecond

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
// `ALTER TABLE ... DELETE WHERE cookie_id = ? AND site_id = ?`
// against each, and returns the per-table result. mutations_sync is
// NOT set — mutations run in the background; the caller acks 202
// after WAL drain and the rows disappear at next merge.
//
// cookieIDHash MUST be the "h:" + hex form that hits events_raw
// post-Stage-1 PR #3. siteID MUST be the visitor's tenant; the
// WHERE clause includes it so a malicious controlled-collision
// attack or insider with master_secret access cannot trigger
// cross-site data destruction (see audit/legal-vs-system-audit.md
// FAIL-1). Empty hash or siteID=0 is rejected.
func (e *EraseEnumerator) EraseByCookieID(ctx context.Context, siteID uint32, cookieIDHash string) ([]EraseResult, error) {
	if cookieIDHash == "" {
		return nil, errEraseEmptyHash
	}

	if siteID == 0 {
		return nil, errEraseEmptySiteID
	}

	tables, err := e.discoverTablesWithCookieID(ctx)
	if err != nil {
		return nil, fmt.Errorf("erase: discover tables: %w", err)
	}

	out := make([]EraseResult, 0, len(tables))

	for _, table := range tables {
		result := EraseResult{Table: table}

		stmt := buildEraseSQL(e.database, table)

		if execErr := e.conn.Exec(ctx, stmt, cookieIDHash, siteID); execErr != nil {
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

// buildEraseSQL produces the per-table erase mutation with the
// site_id filter baked in. Extracted as a helper so tests can pin
// the SQL shape without spinning up ClickHouse (the Exec call is
// covered by integration tests). The two `?` placeholders bind
// cookieIDHash then siteID in that order — Exec callers must pass
// args in the same order.
func buildEraseSQL(database, table string) string {
	return fmt.Sprintf(
		"ALTER TABLE %s.%s DELETE WHERE cookie_id = ? AND site_id = ?",
		quoteIdent(database), quoteIdent(table),
	)
}

// WaitForCompletion polls system.mutations for the ALTER ... DELETE
// mutations dispatched against `tables` since `dispatchedSince`,
// invoking onCompleted when all reach is_done = 1 or onTimeout if ctx
// is cancelled first. Designed to run in a detached goroutine launched
// by the erase handler so the HTTP response can return 202 immediately
// while the audit event for completion still fires.
//
// Why snapshot-by-time instead of capturing mutation IDs from the
// Exec calls: the clickhouse-go driver's Exec returns no result for
// ALTER mutations. Filtering system.mutations by (database, table,
// create_time >= dispatchedSince) reliably surfaces "our" mutation
// IDs even under concurrent erase requests, because each request's
// dispatchedSince is unique to its goroutine.
func (e *EraseEnumerator) WaitForCompletion(
	ctx context.Context,
	tables []string,
	dispatchedSince time.Time,
	onCompleted func(mutationCount int),
	onTimeout func(pendingCount int),
) {
	if len(tables) == 0 {
		onCompleted(0)

		return
	}

	mutationIDs, err := e.snapshotMutationIDs(ctx, tables, dispatchedSince)
	if err != nil || len(mutationIDs) == 0 {
		// CH unreachable or no mutations matched the snapshot window.
		// Emitting Timeout (not Completed) signals "tracking lost" so
		// the operator investigates rather than trusting a false-OK.
		onTimeout(0)

		return
	}

	ticker := time.NewTicker(completionPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Use a fresh background context for the final count so
			// the timeout-attribute value still reflects reality even
			// when the parent ctx is already cancelled.
			pending, _ := e.countPendingMutations(context.Background(), mutationIDs)
			onTimeout(pending)

			return
		case <-ticker.C:
			pending, pollErr := e.countPendingMutations(ctx, mutationIDs)
			if pollErr != nil {
				// Transient CH error — keep polling until ctx
				// deadline. Persistent failure surfaces as Timeout
				// via the ctx.Done branch.
				continue
			}

			if pending == 0 {
				onCompleted(len(mutationIDs))

				return
			}
		}
	}
}

// snapshotMutationIDs captures the mutation_id list created against
// `tables` since `since`. Runs once per erase request right after
// dispatch; the IDs are then polled via countPendingMutations.
//
// Queries against `system.mutations` are exempt from the
// clickhouse-operations-review SETTINGS-max_execution_time rule
// (rule 4): the rule's Semgrep scope is `internal/query/**` +
// `internal/api/**`, and the intent ("every customer SELECT") covers
// tenant-data tables, not internal admin metadata.
func (e *EraseEnumerator) snapshotMutationIDs(
	ctx context.Context,
	tables []string,
	since time.Time,
) ([]string, error) {
	rows, err := e.conn.Query(ctx, `
		SELECT mutation_id
		FROM system.mutations
		WHERE database = ?
		  AND table IN ?
		  AND create_time >= ?
	`, e.database, tables, since)
	if err != nil {
		return nil, fmt.Errorf("erase: snapshot mutations: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := make([]string, 0, len(tables))

	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, scanErr
		}

		out = append(out, id)
	}

	return out, rows.Err()
}

// countPendingMutations returns how many of `mutationIDs` are still
// running (is_done = 0). Used as the loop condition for
// WaitForCompletion. See snapshotMutationIDs above for the
// SETTINGS-exemption rationale.
func (e *EraseEnumerator) countPendingMutations(
	ctx context.Context,
	mutationIDs []string,
) (int, error) {
	var count uint64

	if err := e.conn.QueryRow(ctx, `
		SELECT count()
		FROM system.mutations
		WHERE database = ?
		  AND mutation_id IN ?
		  AND is_done = 0
	`, e.database, mutationIDs).Scan(&count); err != nil {
		return 0, err
	}

	// Mutation count is bounded by len(mutationIDs) — realistically
	// <100, never close to math.MaxInt. Cap defensively before the
	// uint64 → int narrowing (gosec G115).
	if count > uint64(math.MaxInt) {
		count = uint64(math.MaxInt)
	}

	return int(count), nil
}
