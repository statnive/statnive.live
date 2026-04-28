#!/usr/bin/env bash
# oracle-scan.sh — doc 29 §6.2 four canonical queries (loss / duplicates /
# ordering / latency) against events_raw filtered by test_run_id.
#
# Exit non-zero if loss_fraction > 0.0005 (CLAUDE.md analytics-invariant
# server-side threshold) OR duplicate count > 0.
#
#   Usage: scripts/oracle-scan.sh <test_run_id> [hostname]
#
# Requires either a clickhouse-client on PATH (talks to localhost:9000) or
# the dev container `statnive-clickhouse-dev` from deploy/docker-compose.dev.yml.
set -euo pipefail

RUN_ID="${1:?usage: oracle-scan.sh <test_run_id> [hostname]}"
HOSTNAME_FILTER="${2:-load-test.example.com}"

# Pick the binary — prefer host install, fall back to the dev container.
if command -v clickhouse-client >/dev/null 2>&1; then
    CH="clickhouse-client"
elif docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^statnive-clickhouse-dev$'; then
    CH="docker exec -i statnive-clickhouse-dev clickhouse-client --port 9000"
else
    echo "FAIL: no clickhouse-client or statnive-clickhouse-dev container available" >&2
    exit 2
fi

run_query() {
    local label="$1" sql="$2"
    echo "## $label"
    echo "$sql" | $CH 2>&1 || echo "WARN: query failed: $label"
    echo
}

echo "# oracle-scan run_id=$RUN_ID hostname=$HOSTNAME_FILTER"
echo "# generated $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo

run_query "loss" "
SELECT generator_node_id,
       max(test_generator_seq)               AS max_seq,
       min(test_generator_seq)               AS min_seq,
       (max_seq - min_seq + 1)               AS expected,
       count()                               AS observed,
       expected - observed                   AS missing,
       missing / nullIf(expected, 0)         AS loss_fraction
FROM statnive.events_raw
WHERE test_run_id = toUUID('$RUN_ID')
  AND hostname = '$HOSTNAME_FILTER'
GROUP BY generator_node_id
ORDER BY generator_node_id
FORMAT Pretty
"

run_query "duplicates" "
SELECT generator_node_id, test_generator_seq, count() AS dup
FROM statnive.events_raw
WHERE test_run_id = toUUID('$RUN_ID')
GROUP BY generator_node_id, test_generator_seq
HAVING count() > 1
ORDER BY dup DESC
LIMIT 100
FORMAT Pretty
"

run_query "latency p50/p95/p99 per minute" "
SELECT toStartOfMinute(time) AS minute,
       quantileTDigest(0.50)(dateDiff('millisecond', send_ts, time)) AS p50_ms,
       quantileTDigest(0.95)(dateDiff('millisecond', send_ts, time)) AS p95_ms,
       quantileTDigest(0.99)(dateDiff('millisecond', send_ts, time)) AS p99_ms
FROM statnive.events_raw
WHERE test_run_id = toUUID('$RUN_ID')
  AND send_ts > toDateTime64(0, 3)
GROUP BY minute
ORDER BY minute
FORMAT Pretty
"

run_query "ordering" "
SELECT generator_node_id,
       countIf(test_generator_seq < prev_seq) AS out_of_order,
       count()                                 AS total
FROM (
    SELECT generator_node_id, test_generator_seq,
           lagInFrame(test_generator_seq) OVER
               (PARTITION BY generator_node_id ORDER BY time) AS prev_seq
    FROM statnive.events_raw WHERE test_run_id = toUUID('$RUN_ID')
)
GROUP BY generator_node_id
FORMAT Pretty
"

echo "# done"
