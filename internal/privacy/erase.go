package privacy

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

var (
	errEraseEmptyHash   = errors.New("erase: empty cookie hash")
	errEraseEmptySiteID = errors.New("erase: site_id is 0")
	errEraseEmptyUserID = errors.New("erase: user_id is nil")
)

// oauthGrantTables are the migration-023 OAuth tables that key a grant to the
// consenting dashboard user (Article 17 erasure targets). oauth_clients is
// excluded — it is operator-owned (the registered ChatGPT connector), not the
// data subject's PII.
var oauthGrantTables = []string{"oauth_auth_codes", "oauth_refresh_tokens"}

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

// EraseOAuthGrantsByUserID issues `ALTER TABLE ... DELETE WHERE user_id = ?`
// against the OAuth grant tables (migration 023), erasing every authorization
// code + refresh token tied to userID. The OAuth AS (chatgpt_app build) writes
// user_id from the consenting dashboard account, so a user-account deletion /
// user-scoped DSAR request MUST call this — otherwise those grants outlive the
// data subject (GDPR Art. 17).
//
// Build-agnostic on purpose: plain SQL on driver.Conn, no chatgpt_app tag, so
// the user-scoped account eraser can invoke it in EVERY build (the tables exist
// in every build via migration 023; they are simply empty unless the AS ran).
// The single `?` binds userID — never string-concatenated. Like EraseByCookieID,
// mutations_sync is NOT set; the rows disappear at next merge and
// WaitForCompletion can be used to confirm.
//
// NOTE (cross-branch): the user-scoped account eraser (EraseByUserID) lands on
// the PR-A token stack (#188); its integration checklist MUST call this method.
// Tracked in LEARN.md + the migration-023 header so the wiring is not dropped at
// merge. See the dsar-completeness-checker contract.
func (e *EraseEnumerator) EraseOAuthGrantsByUserID(ctx context.Context, userID uuid.UUID) ([]EraseResult, error) {
	if userID == uuid.Nil {
		return nil, errEraseEmptyUserID
	}

	out := make([]EraseResult, 0, len(oauthGrantTables))

	for _, table := range oauthGrantTables {
		result := EraseResult{Table: table}

		stmt := fmt.Sprintf(
			"ALTER TABLE %s.%s DELETE WHERE user_id = ?",
			quoteIdent(e.database), quoteIdent(table),
		)

		if execErr := e.conn.Exec(ctx, stmt, userID); execErr != nil {
			result.Err = execErr.Error()
		} else {
			result.MutationSent = true
		}

		out = append(out, result)
	}

	return out, nil
}

// EraseByUserID is the single account-scoped erasure entry point: it purges
// EVERY dashboard-user-linked store for userID — self-serve MCP tokens
// (mcp_tokens, via EraseTokensByUserID) AND OAuth grants (oauth_auth_codes +
// oauth_refresh_tokens, via EraseOAuthGrantsByUserID).
//
// An account-deletion / user-scoped DSAR path MUST call THIS rather than the
// individual erasers, so a future user_id-keyed table can't be silently left
// out of erasure — the exact bug class LEARN.md Lesson 40 records (a new table
// added without an erase path). This is also the prod-enable gate referenced in
// docs/runbook.md § "ChatGPT-app OAuth AS — go-live": confirm the account-erase
// path calls privacy.EraseByUserID before enabling oauth_as in a GDPR
// deployment.
//
// Token erasure runs first and aborts the call on error (returned wrapped) so a
// partial erase is visible; otherwise the per-table oauth results are returned.
func (e *EraseEnumerator) EraseByUserID(ctx context.Context, userID uuid.UUID) ([]EraseResult, error) {
	if userID == uuid.Nil {
		return nil, errEraseEmptyUserID
	}

	if err := e.EraseTokensByUserID(ctx, userID); err != nil {
		return nil, fmt.Errorf("erase mcp tokens for user %s: %w", userID, err)
	}

	return e.EraseOAuthGrantsByUserID(ctx, userID)
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
