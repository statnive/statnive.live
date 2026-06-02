-- 020_segments_props.sql — three Map columns on events_raw for custom
-- dimensions at hit / session / user scope (Phase 2 of segments + A/B
-- compare). Hit-scope mirrors data carried in the existing prop_keys /
-- prop_vals parallel arrays for one release; session + user scopes are
-- new concepts surfaced by Phase 1's tracker setSession + identify
-- (uid, userProps) public API.
--
-- Architecture Rule 5 compliance: no Nullable. Maps default to map() and
-- compress sparsely (>90% empty in steady state per the plan's storage
-- envelope; ClickHouse's sparse-serialisation handles this at ~0 disk
-- cost — see CLAUDE.md Architecture Rule 5 carve-out reasoning).
--
-- Architecture Rule 8 compliance: events_raw's ORDER BY (site_id, time,
-- ...) is unchanged. The new columns are payload-only — they do not
-- participate in the primary key. Filtered queries over the Map columns
-- live on the "raw-fallback path" carved out in the plan's § 4 Caching
-- + cost guardrails block (cached 1h, range capped to 90 days).
--
-- Cluster templating: `{{if .Cluster}} ON CLUSTER {{.Cluster}} {{end}}`
-- is interpolated by migrate.go so single-node and ReplicatedMergeTree
-- modes share one schema. Doc 24 §Migration 0029.
--
-- The 3 new columns ride at the END of the events_raw column list,
-- matching the load-gate oracle columns added by migration 018. Adding
-- AT THE END keeps existing sparse-column offsets stable so the
-- ClickHouse data parts already on disk do not need rewriting.
--
-- Bloom-filter indexes on mapKeys() are intentionally deferred to a
-- follow-up. The raw-fallback path uses linear scan with the 90-day
-- range cap as the primary cost guardrail; adding bloom indexes lands
-- as a perf optimisation once we observe enough operator-driven prop
-- traffic to justify the index maintenance cost.

ALTER TABLE statnive.events_raw{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS hit_props     Map(LowCardinality(String), String) DEFAULT map() CODEC(ZSTD(3)),
    ADD COLUMN IF NOT EXISTS session_props Map(LowCardinality(String), String) DEFAULT map() CODEC(ZSTD(3)),
    ADD COLUMN IF NOT EXISTS user_props    Map(LowCardinality(String), String) DEFAULT map() CODEC(ZSTD(3));
