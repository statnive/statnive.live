-- 018_load_gate_oracle_columns.sql — Phase 7e load-gate oracle columns.
--
-- Adds 4 columns to events_raw that the test/perf/generator/ tool fills
-- on every synthesized event. The columns let one ClickHouse query
-- compute loss / duplicates / ordering / latency for a load run by
-- matching what the generator sent against what the ingest pipeline
-- materialized. PLAN.md Phase 7e § B.1; doc 29 §6.1 + §6.2.
--
-- ## Column shapes (CLAUDE.md Architecture Rule 5 carve-out)
--
-- All four are typed-default sentinels, NOT Nullable. doc 29 §6.1 proves
-- the sparse-serialization path is ~zero-cost in production because
-- non-load-gate traffic always writes the default (>93.75% defaults
-- threshold). Nullable would cost 10–200% on aggregations per CLAUDE.md
-- Architecture Rule 5.
--
--   test_run_id          UUID DEFAULT toUUIDOrZero('') — the load run that
--                        emitted this event; toUUIDOrZero('') returns the
--                        all-zero UUID (NOT NULL — would break the non-
--                        Nullable column constraint), our "this event
--                        was not part of a load gate run" sentinel
--   test_generator_seq   UInt64 DEFAULT 0 — monotonically increasing per
--                        (test_run_id, generator_node_id); the primary
--                        loss-detection primitive (any gap = lost event)
--   generator_node_id    UInt16 DEFAULT 0 — which generator instance
--                        emitted this row; multi-node generators sit
--                        behind one test_run_id at different nodes
--   send_ts              DateTime64(3) DEFAULT 0 — millisecond-resolution
--                        timestamp captured at generator-send (NOT
--                        receive); ingest_latency = time - send_ts
--
-- ## Projection proj_oracle
--
-- Sub-second per-run aggregations on 200M-row events_raw runs (doc 29
-- §6.2). ORDER BY (test_run_id, generator_node_id, test_generator_seq)
-- so the oracle queries hit a single sorted range per (run, node) tuple.
-- Without this projection the oracle queries scan the full table.
--
-- The projection has the same TTL as events_raw — when events_raw rows
-- expire, the projection rows expire with them. No separate retention.
--
-- ## Backfill
--
-- Existing rows get the DEFAULT values via ClickHouse's lazy backfill
-- (ALTER ADD COLUMN with DEFAULT is metadata-only; values materialize on
-- first read or first part merge). No UPDATE needed.
--
-- ## Forward-only
--
-- Project convention — no paired down migration. To revert manually:
--   ALTER TABLE statnive.events_raw DROP PROJECTION IF EXISTS proj_oracle;
--   ALTER TABLE statnive.events_raw DROP COLUMN IF EXISTS send_ts;
--   ALTER TABLE statnive.events_raw DROP COLUMN IF EXISTS generator_node_id;
--   ALTER TABLE statnive.events_raw DROP COLUMN IF EXISTS test_generator_seq;
--   ALTER TABLE statnive.events_raw DROP COLUMN IF EXISTS test_run_id;
-- Ingest code paths that write these columns must be reverted first.

ALTER TABLE statnive.events_raw{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS test_run_id UUID DEFAULT toUUIDOrZero('') CODEC(ZSTD(3));

ALTER TABLE statnive.events_raw{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS test_generator_seq UInt64 DEFAULT 0 CODEC(Delta(8), ZSTD(1));

ALTER TABLE statnive.events_raw{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS generator_node_id UInt16 DEFAULT 0 CODEC(ZSTD(1));

ALTER TABLE statnive.events_raw{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS send_ts DateTime64(3) DEFAULT 0 CODEC(Delta(8), ZSTD(1));

-- proj_oracle — sub-second loss / dupes / ordering / latency on per-run
-- slices. The projection's ORDER BY mirrors what oracle_queries.sql
-- groups + sorts by, so each query is a single sorted-range scan.
ALTER TABLE statnive.events_raw{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD PROJECTION IF NOT EXISTS proj_oracle (
        SELECT
            test_run_id,
            generator_node_id,
            test_generator_seq,
            send_ts,
            time,
            site_id
        ORDER BY (test_run_id, generator_node_id, test_generator_seq)
    );

-- Materialize the projection for existing parts. New parts pick it up
-- automatically; without this call, the projection is empty for rows
-- that already exist when the migration runs.
ALTER TABLE statnive.events_raw{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MATERIALIZE PROJECTION proj_oracle;
