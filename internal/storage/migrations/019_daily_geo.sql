-- 019_daily_geo.sql — country/region/city visitor + revenue rollup.
-- Powers /api/stats/geo (PLAN.md v1.1-geo). Templated for cluster mode
-- like the other v1 rollups (migration 002).
--
-- ORDER BY leads with site_id per Architecture Rule 8. TTL ships
-- inline at 750 DAY DELETE to match migration 011's CNIL Sheet n°16
-- aggregated-audience-measurement cap (25 months).
--
-- The MV catches every new event the moment migration 019 applies.
-- Historical events_raw rows are loaded via cmd/geo-backfill — never
-- inline here, so this migration stays sub-second on production-size
-- events_raw (Production Safety contract in PLAN.md v1.1-geo).
--
-- LEARN.md Lesson 30: the IP2Location LITE "parameter unavailable"
-- sentinel is filtered upstream by cleanGeoField in
-- internal/enrich/geoip.go before any event reaches events_raw, so
-- daily_geo inherits clean province / city values. Test guard at
-- internal/storage/queries_test.go::TestGeo_NoLITESentinelLanding.
--
-- Numeric columns (views / goals / revenue) MUST be wrapped in
-- SimpleAggregateFunction(sum, …) — migration 009 codified this rule
-- after the v1 rollups initially shipped with plain UInt64, which
-- AggregatingMergeTree silently collapses to a non-deterministic
-- last-value-wins on merge. CH auto-wraps the MV's `count()` /
-- `sum(...)` UInt64 expressions into the column's
-- SimpleAggregateFunction type at INSERT time, so the MV SELECT
-- doesn't change shape.

CREATE TABLE IF NOT EXISTS statnive.daily_geo{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    site_id        UInt32,
    day            Date,
    country_code   FixedString(2),
    province       LowCardinality(String),
    city           LowCardinality(String),
    views          SimpleAggregateFunction(sum, UInt64),
    visitors_state AggregateFunction(uniqCombined64, FixedString(16)),
    goals          SimpleAggregateFunction(sum, UInt64),
    revenue        SimpleAggregateFunction(sum, UInt64)
)
ENGINE = {{if .Cluster}}ReplicatedAggregatingMergeTree('/clickhouse/tables/{shard}/daily_geo', '{replica}'){{else}}AggregatingMergeTree(){{end}}
PARTITION BY toYYYYMM(day)
ORDER BY (site_id, day, country_code, province, city)
TTL day + INTERVAL 750 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS statnive.mv_daily_geo{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
TO statnive.daily_geo AS
SELECT
    site_id,
    toDate(time) AS day,
    country_code,
    province,
    city,
    count() AS views,
    uniqCombined64State(visitor_hash) AS visitors_state,
    sum(is_goal) AS goals,
    sum(event_value) AS revenue
FROM statnive.events_raw
WHERE is_bot = 0
GROUP BY site_id, day, country_code, province, city;
