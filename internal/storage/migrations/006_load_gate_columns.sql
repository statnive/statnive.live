-- 006_load_gate_columns.sql — Phase 7e generator_seq oracle (PLAN.md §283).
-- Sparse-column instrumentation for load-gate test runs. Production cost ≈
-- zero at >93.75% defaults — sparse serialization kicks in automatically and
-- compresses a column whose values are mostly the default sentinel down to
-- O(non-default rows), per CH MergeTree settings.
--
-- Architecture Rule 5 carve-out (CLAUDE.md): test-instrumentation columns use
-- typed DEFAULT sentinels (UUID zero / UInt64 0 / UInt16 0 / DateTime64(3) 0)
-- — never Nullable(...). Doc 29 §6.1's Nullable schema is overridden here.
--
-- Codecs: ZSTD(1) for the run/node identifiers, Delta+ZSTD(1) for the
-- monotonic seq + send_ts (~10× compression on dense ascending integers).
--
-- No inline column comments — the migration runner strips full-line "--"
-- comments but the clickhouse-go prepared-statement path miscounts parens
-- on inline "-- ...(..)" tails (lesson from migration 004, repeated in 005).

ALTER TABLE statnive.events_raw{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS test_run_id        UUID            DEFAULT toUUID('00000000-0000-0000-0000-000000000000') CODEC(ZSTD(1)),
    ADD COLUMN IF NOT EXISTS generator_node_id  UInt16          DEFAULT 0                                              CODEC(ZSTD(1)),
    ADD COLUMN IF NOT EXISTS test_generator_seq UInt64          DEFAULT 0                                              CODEC(Delta, ZSTD(1)),
    ADD COLUMN IF NOT EXISTS send_ts            DateTime64(3)   DEFAULT toDateTime64(0, 3)                             CODEC(Delta, ZSTD(1));

-- proj_oracle: sub-second loss/dup/ordering scans on (run, node, seq).
-- ORDER BY pattern aligned with doc 29 §6.1 — projection materialization
-- happens after the columns exist (separate ALTER for clarity + retry).

ALTER TABLE statnive.events_raw{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD PROJECTION IF NOT EXISTS proj_oracle (
        SELECT test_run_id, generator_node_id, test_generator_seq, time, send_ts
        ORDER BY (test_run_id, generator_node_id, test_generator_seq)
    );

ALTER TABLE statnive.events_raw{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MATERIALIZE PROJECTION proj_oracle;
