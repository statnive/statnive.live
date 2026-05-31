-- oracle_queries.sql — the 4 canonical Phase 7e gate queries.
--
-- Each query takes one parameter: ${TEST_RUN_ID} (UUID). Substitute via
-- clickhouse-client --param-test_run_id=<uuid> or via the make
-- oracle-scan target which envsubsts the value in. All four queries
-- hit the proj_oracle projection so they run in <1 s on 200M-row
-- events_raw tables (doc 29 §6.2).
--
-- SLO budgets (PLAN.md Phase 7e exit criteria + CLAUDE.md
-- "Analytics Invariant Thresholds"):
--   loss_server ≤ 0.05% of sent
--   duplicates ≤ 0.1% of sent
--   ordering_inversions = 0 within per-(node) sequence stream
--   p99 latency < 2000ms; p99.9 < 5000ms
--
-- Each query returns ONE row (one summary line per run). Aggregating
-- happens at the bash side: `make oracle-scan` runs all 4 and reduces
-- to a pass/fail summary plus a JSON evidence dump.

-- ====================================================================
-- Q1 — LOSS: how many sequence numbers did we send vs receive?
-- The generator's max seq per (run, node) is monotonically increasing
-- so max - count = the count of dropped events.
-- ====================================================================
SELECT
    generator_node_id,
    max(test_generator_seq)             AS max_seq_sent,
    count()                             AS rows_received,
    max(test_generator_seq) - count()   AS loss_count,
    if(max(test_generator_seq) > 0,
       (max(test_generator_seq) - count()) / max(test_generator_seq) * 100,
       0)                               AS loss_pct
FROM statnive.events_raw
WHERE test_run_id = {test_run_id:UUID}
GROUP BY generator_node_id
ORDER BY generator_node_id;

-- ====================================================================
-- Q2 — DUPLICATES: ingest must be exactly-once per send. Anything that
-- shows the same (test_run_id, generator_node_id, test_generator_seq)
-- twice = a duplicate.
-- ====================================================================
SELECT
    generator_node_id,
    count()                                    AS rows_received,
    uniqExact(test_generator_seq)              AS unique_seqs,
    count() - uniqExact(test_generator_seq)    AS dup_count,
    if(count() > 0,
       (count() - uniqExact(test_generator_seq)) / count() * 100,
       0)                                      AS dup_pct
FROM statnive.events_raw
WHERE test_run_id = {test_run_id:UUID}
GROUP BY generator_node_id
ORDER BY generator_node_id;

-- ====================================================================
-- Q3 — ORDERING: within one (run, node) stream the receive-order must
-- match the send-order modulo WAL batching. Count seq pairs where the
-- earlier-sent row arrived LATER (time-wise) than the later-sent row.
-- Inversions > 0 means batch boundaries or async ingest reordered the
-- stream, which breaks funnel windowFunnel() determinism.
-- ====================================================================
SELECT
    generator_node_id,
    countIf(time < lagInFrame(time) OVER w)    AS inversion_count,
    count()                                    AS rows_examined
FROM statnive.events_raw
WHERE test_run_id = {test_run_id:UUID}
WINDOW w AS (PARTITION BY generator_node_id ORDER BY test_generator_seq)
GROUP BY generator_node_id
ORDER BY generator_node_id;

-- ====================================================================
-- Q4 — LATENCY: end-to-end p50 / p95 / p99 / p99.9 (ms) from generator-
-- send to ClickHouse-visible. Quantiles use the t-digest variant for
-- bounded memory at high cardinality.
--
-- toUnixTimestamp64Milli(send_ts) − toUnixTimestamp(time)*1000 gives the
-- ms-resolution diff; send_ts is DateTime64(3), time is DateTime
-- (1 s resolution) so latencies < 1 s show as 0—999 ms.
-- ====================================================================
SELECT
    count()                                                                       AS rows_sampled,
    quantileTDigest(0.5)(toUnixTimestamp(time) * 1000 - toUnixTimestamp64Milli(send_ts))   AS p50_ms,
    quantileTDigest(0.95)(toUnixTimestamp(time) * 1000 - toUnixTimestamp64Milli(send_ts))  AS p95_ms,
    quantileTDigest(0.99)(toUnixTimestamp(time) * 1000 - toUnixTimestamp64Milli(send_ts))  AS p99_ms,
    quantileTDigest(0.999)(toUnixTimestamp(time) * 1000 - toUnixTimestamp64Milli(send_ts)) AS p999_ms,
    max(toUnixTimestamp(time) * 1000 - toUnixTimestamp64Milli(send_ts))                    AS max_ms
FROM statnive.events_raw
WHERE test_run_id = {test_run_id:UUID}
  AND send_ts > toDateTime64(0, 3);
