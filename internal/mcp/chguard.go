package mcp

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// ClickHouse per-call cost guards. These bound any single MCP query at the DB
// layer, complementing the app-layer limit clamp + per-actor budgets
// (defense in depth). Values mirror the clickhouse-operations-review mandate.
const (
	chMaxExecutionTime = 30          // seconds (the MCP per-call DB timeout)
	chMaxRowsToRead    = 200_000_000 // hard stop on a runaway scan
	chMaxMemoryUsage   = 2_000_000_000
	chMaxThreads       = 4
)

// withCHGuards attaches cost-guard SETTINGS + a unique query_id to the context
// a scoped tool handler hands to Store. Per-query settings merge over the
// connection settings (query-level wins), confirmed against the clickhouse-go
// v2 API. maxResultRows is the clamped row limit (0 ⇒ scan-cap only). The
// query_id (unique per call) tags MCP queries in system.query_log for the
// Phase-10 CH quota + forensics.
func withCHGuards(ctx context.Context, queryID string, maxResultRows int) context.Context {
	settings := clickhouse.Settings{
		"max_execution_time":    chMaxExecutionTime,
		"max_rows_to_read":      chMaxRowsToRead,
		"read_overflow_mode":    "break",
		"timeout_overflow_mode": "break",
		"max_memory_usage":      chMaxMemoryUsage,
		"max_threads":           chMaxThreads,
	}

	if maxResultRows > 0 {
		settings["max_result_rows"] = maxResultRows
		settings["result_overflow_mode"] = "break"
	}

	return clickhouse.Context(ctx, clickhouse.WithSettings(settings), clickhouse.WithQueryID(queryID))
}
