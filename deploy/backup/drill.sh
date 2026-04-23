#!/usr/bin/env bash
# Automated backup-restore drill — operator SOP in docs/runbook.md § Backup & restore.
#
# Usage:
#   drill.sh --config=PATH --mode=full|incremental [--name=NAME]
#
# Exit 0: all tables pass row-count parity + rollup mergeability.
# Exit 1: any table fails, or any step errors.
#
# Env:
#   CH_CLIENT           clickhouse-client binary (default: clickhouse-client)
#   CH_HOST             drill CH host (default: 127.0.0.1)
#   CH_PORT             drill CH TCP port (default: 9000)
#   CLICKHOUSE_BACKUP   clickhouse-backup binary (default: clickhouse-backup)
#   EXPECT_ROWS         optional "TABLE=N,TABLE=N" pre-backup snapshot; when
#                       unset, the drill asserts non-zero rows instead.
#   DRILL_TABLES        space-separated table list (default:
#                       "events_raw hourly_visitors daily_pages daily_sources")
set -euo pipefail

CONFIG=""
MODE="full"
NAME=""

for arg in "$@"; do
  case "$arg" in
    --config=*) CONFIG="${arg#*=}" ;;
    --mode=*)   MODE="${arg#*=}" ;;
    --name=*)   NAME="${arg#*=}" ;;
    -h|--help)
      sed -n '2,/^set -euo/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "unknown arg: $arg" >&2; exit 1 ;;
  esac
done

[ -n "$CONFIG" ] || { echo "FAIL: --config=PATH is required" >&2; exit 1; }
[ -f "$CONFIG" ] || { echo "FAIL: config not found at $CONFIG" >&2; exit 1; }

CH_CLIENT="${CH_CLIENT:-clickhouse-client}"
CH_HOST="${CH_HOST:-127.0.0.1}"
CH_PORT="${CH_PORT:-9000}"
CLICKHOUSE_BACKUP="${CLICKHOUSE_BACKUP:-clickhouse-backup}"
DRILL_TABLES="${DRILL_TABLES:-events_raw hourly_visitors daily_pages daily_sources}"

ch_query() {
  "$CH_CLIENT" --host "$CH_HOST" --port "$CH_PORT" -q "$1"
}

if [ -z "$NAME" ]; then
  NAME=$("$CLICKHOUSE_BACKUP" --config "$CONFIG" list remote | awk 'NR==1 {print $1}')
fi
[ -n "$NAME" ] || { echo "FAIL: no remote backups found" >&2; exit 1; }
echo "drill: restoring '$NAME' (mode=$MODE)"

"$CLICKHOUSE_BACKUP" --config "$CONFIG" restore_remote "$NAME"

# One query returns every table's active row count — avoids N round-trips.
tables_sql=$(echo "$DRILL_TABLES" | tr ' ' '\n' | awk '{printf "%s\x27%s\x27", (NR>1?",":""), $0}')
counts=$(ch_query "SELECT table, sum(rows) FROM system.parts WHERE table IN ($tables_sql) AND active GROUP BY table FORMAT TSV")

fail=0
for t in $DRILL_TABLES; do
  drill_rows=$(echo "$counts" | awk -v t="$t" '$1==t {print $2}')
  drill_rows="${drill_rows:-0}"

  expected=""
  if [ -n "${EXPECT_ROWS:-}" ]; then
    expected=$(echo "$EXPECT_ROWS" | tr ',' '\n' | awk -F= -v t="$t" '$1==t {print $2}')
  fi

  if [ -n "$expected" ]; then
    if [ "$drill_rows" = "$expected" ]; then
      echo "$t OK ($drill_rows rows)"
    else
      echo "$t FAIL expected=$expected drill=$drill_rows"; fail=1
    fi
  else
    if [ "$drill_rows" -gt 0 ] 2>/dev/null; then
      echo "$t OK ($drill_rows rows)"
    else
      echo "$t FAIL empty (drill=$drill_rows)"; fail=1
    fi
  fi
done

# Rollup mergeability — catches AggregateFunction-state corruption that
# row-count alone would miss. FORMAT Null silences result; non-zero exit
# means the merge errored.
if ch_query "SELECT countMerge(visitors_hll_state) FROM hourly_visitors FINAL FORMAT Null" >/dev/null 2>&1; then
  echo "hourly_visitors rollup OK (countMerge succeeded)"
else
  echo "hourly_visitors rollup FAIL countMerge errored — AggregateFunction state may be corrupt"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "drill: FAIL — see above" >&2
  exit 1
fi
echo "drill: PASS — all tables parity + rollup mergeability green"
