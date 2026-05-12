-- 011_rollup_ttl.sql — 750-day TTL on the three v1 AggregatingMergeTree
-- rollups. CNIL audience-measurement exemption Sheet n°16 caps
-- aggregated visitor data at 25 months; 750d ≈ 24.6 months gives margin.
--
-- Forward-acting only: existing rows survive until they naturally age
-- past the 750-day window relative to the partition's hour/day column.
-- ClickHouse's merge worker enforces TTL during background merges;
-- nothing is dropped synchronously at ALTER time.
--
-- Engine + ON CLUSTER are templated for the same single-node →
-- Distributed migration story as migration 002.
--
-- Rollback: a future migration sets MODIFY TTL <column> + INTERVAL
-- 100000 DAY DELETE (effectively disables TTL). No down-migration
-- because the runner is forward-only by design.

ALTER TABLE statnive.hourly_visitors{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY TTL hour + INTERVAL 750 DAY DELETE;

ALTER TABLE statnive.daily_pages{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY TTL day + INTERVAL 750 DAY DELETE;

ALTER TABLE statnive.daily_sources{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY TTL day + INTERVAL 750 DAY DELETE;
