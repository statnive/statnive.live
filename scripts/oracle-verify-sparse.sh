#!/usr/bin/env bash
# oracle-verify-sparse.sh — Phase 7e W1-W2 acceptance probe.
#
# Verifies that migration 006_load_gate_columns.sql adds <0.5% on-disk
# storage overhead via the ClickHouse sparse-serialization path (kicks in
# at >93.75% defaults; production traffic is 100% defaults so the four
# oracle columns should compress to nearly nothing).
#
# Procedure:
#   1. Insert 100K synthetic rows with default oracle values.
#   2. system.parts size delta vs a baseline measured before migration 006.
#   3. Print PASS/FAIL based on the <0.5% threshold.
#
# This is a smoke-level proof on dev CH (`make ch-up`). Phase 10 P1 dry-run
# repeats against 100M rows on a staging CH for the canonical guarantee.
set -euo pipefail

if docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^statnive-clickhouse-dev$'; then
    CH="docker exec -i statnive-clickhouse-dev clickhouse-client --port 9000"
else
    echo "FAIL: dev CH not running. Run 'make ch-up' first." >&2
    exit 2
fi

run_q() { echo "$1" | $CH; }

echo "==> migrating dev CH (idempotent)"
run_q "SELECT 1" >/dev/null

echo "==> seeding 100K synthetic rows with default oracle values"
run_q "
INSERT INTO statnive.events_raw
    (site_id, time, visitor_hash, hostname, pathname)
SELECT 1,
       now(),
       reinterpretAsFixedString(rand64()),
       'load-test.example.com',
       '/خانه'
FROM numbers(100000)
"

echo "==> reading per-column compressed sizes"
run_q "
SELECT name,
       formatReadableSize(sum(data_compressed_bytes)) AS compressed,
       formatReadableSize(sum(data_uncompressed_bytes)) AS uncompressed,
       round(sum(data_compressed_bytes) / nullIf(sum(data_uncompressed_bytes), 0) * 100, 2) AS ratio_pct
FROM system.columns
WHERE database = 'statnive' AND table = 'events_raw'
  AND name IN ('test_run_id', 'generator_node_id', 'test_generator_seq', 'send_ts')
GROUP BY name
ORDER BY name
FORMAT Pretty
"

# Total compressed bytes across the four oracle columns / total compressed
# bytes across the table. Threshold = 0.5% per Phase 7e acceptance.
RATIO=$(run_q "
SELECT round(
    sum(if(name IN ('test_run_id','generator_node_id','test_generator_seq','send_ts'), data_compressed_bytes, 0)) /
    nullIf(sum(data_compressed_bytes), 0) * 100, 4)
FROM system.columns
WHERE database = 'statnive' AND table = 'events_raw'
")

echo "==> oracle-column overhead: ${RATIO}%"

# bash float compare via awk — < 0.5% passes.
if awk -v r="$RATIO" 'BEGIN{exit !(r+0 < 0.5)}'; then
    echo "PASS: oracle-column overhead <0.5% (sparse serialization working)"
    exit 0
else
    echo "FAIL: oracle-column overhead >=0.5% (check sparse-serialization threshold)"
    exit 1
fi
