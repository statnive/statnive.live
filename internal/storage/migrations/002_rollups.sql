-- 002_rollups.sql — three v1 rollups + materialized views.
-- AggregatingMergeTree with uniqCombined64State (HyperLogLog ~0.5% error,
-- 2-3x lower memory than uniqExact). All ORDER BY leads with site_id
-- (Architecture Rule 8). Engine + ON CLUSTER are templated for future
-- single-node → Distributed migration (PLAN.md verification 26).
-- Additional rollups (daily_geo, daily_devices, daily_users) ship in v1.1
-- once the corresponding dashboard panels exist.

-- hourly_visitors: powers /api/stats/overview trend + realtime widget.
CREATE TABLE IF NOT EXISTS statnive.hourly_visitors{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    site_id        UInt32,
    hour           DateTime('UTC'),
    pageviews      UInt64,
    visitors_state AggregateFunction(uniqCombined64, FixedString(16)),
    goals          UInt64,
    revenue_rials  UInt64
)
ENGINE = {{if .Cluster}}ReplicatedAggregatingMergeTree('/clickhouse/tables/{shard}/hourly_visitors', '{replica}'){{else}}AggregatingMergeTree(){{end}}
PARTITION BY toYYYYMM(hour)
ORDER BY (site_id, hour);

CREATE MATERIALIZED VIEW IF NOT EXISTS statnive.mv_hourly_visitors{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
TO statnive.hourly_visitors AS
SELECT
    site_id,
    toStartOfHour(time) AS hour,
    count() AS pageviews,
    uniqCombined64State(visitor_hash) AS visitors_state,
    sum(is_goal) AS goals,
    sum(event_value) AS revenue_rials
FROM statnive.events_raw
WHERE is_bot = 0 AND (event_type = 'pageview' OR is_goal = 1)
GROUP BY site_id, hour;

-- daily_pages: top pages, page-level revenue/goals.
CREATE TABLE IF NOT EXISTS statnive.daily_pages{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    site_id        UInt32,
    day            Date,
    pathname       String,
    views          UInt64,
    visitors_state AggregateFunction(uniqCombined64, FixedString(16)),
    goals          UInt64,
    revenue_rials  UInt64
)
ENGINE = {{if .Cluster}}ReplicatedAggregatingMergeTree('/clickhouse/tables/{shard}/daily_pages', '{replica}'){{else}}AggregatingMergeTree(){{end}}
PARTITION BY toYYYYMM(day)
ORDER BY (site_id, day, pathname);

CREATE MATERIALIZED VIEW IF NOT EXISTS statnive.mv_daily_pages{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
TO statnive.daily_pages AS
SELECT
    site_id,
    toDate(time) AS day,
    pathname,
    count() AS views,
    uniqCombined64State(visitor_hash) AS visitors_state,
    sum(is_goal) AS goals,
    sum(event_value) AS revenue_rials
FROM statnive.events_raw
WHERE is_bot = 0
GROUP BY site_id, day, pathname;

-- daily_sources: sources, channels, UTM, campaign attribution. Powers RPV.
CREATE TABLE IF NOT EXISTS statnive.daily_sources{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    site_id        UInt32,
    day            Date,
    referrer_name  LowCardinality(String),
    channel        LowCardinality(String),
    utm_source     LowCardinality(String),
    utm_medium     LowCardinality(String),
    utm_campaign   String,
    views          UInt64,
    visitors_state AggregateFunction(uniqCombined64, FixedString(16)),
    goals          UInt64,
    revenue_rials  UInt64
)
ENGINE = {{if .Cluster}}ReplicatedAggregatingMergeTree('/clickhouse/tables/{shard}/daily_sources', '{replica}'){{else}}AggregatingMergeTree(){{end}}
PARTITION BY toYYYYMM(day)
ORDER BY (site_id, day, channel, referrer_name, utm_source, utm_medium);

CREATE MATERIALIZED VIEW IF NOT EXISTS statnive.mv_daily_sources{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
TO statnive.daily_sources AS
SELECT
    site_id,
    toDate(time) AS day,
    referrer_name,
    channel,
    utm_source,
    utm_medium,
    utm_campaign,
    count() AS views,
    uniqCombined64State(visitor_hash) AS visitors_state,
    sum(is_goal) AS goals,
    sum(event_value) AS revenue_rials
FROM statnive.events_raw
WHERE is_bot = 0
GROUP BY site_id, day, referrer_name, channel, utm_source, utm_medium, utm_campaign;
