-- 008_rename_revenue_columns.sql — currency-neutral column names.
--
-- Renames `revenue_rials` → `revenue` on the three rollup target tables
-- and `value_rials` → `value` on the goals table, then updates each
-- materialized view's SELECT alias via MODIFY QUERY so future inserts
-- write into the renamed column.
--
-- Lossless: RENAME COLUMN is metadata-only in ClickHouse (no row
-- rewrite); MODIFY QUERY only changes how new rows from events_raw are
-- aliased into the target — historical aggregate state is preserved.
-- Pre/post aggregate equality oracle in test/data_preservation_e2e_test.go.
--
-- Requires ClickHouse ≥22.7 for ALTER TABLE … MODIFY QUERY on
-- materialized views; integration test asserts version at startup.
-- Templated for single-node + Distributed using the same convention as
-- 001/002/003/006/007.

ALTER TABLE statnive.hourly_visitors{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    RENAME COLUMN IF EXISTS revenue_rials TO revenue;

ALTER TABLE statnive.daily_pages{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    RENAME COLUMN IF EXISTS revenue_rials TO revenue;

ALTER TABLE statnive.daily_sources{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    RENAME COLUMN IF EXISTS revenue_rials TO revenue;

ALTER TABLE statnive.goals{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    RENAME COLUMN IF EXISTS value_rials TO value;

ALTER TABLE statnive.mv_hourly_visitors{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY QUERY
SELECT
    site_id,
    toStartOfHour(time) AS hour,
    count() AS pageviews,
    uniqCombined64State(visitor_hash) AS visitors_state,
    sum(is_goal) AS goals,
    sum(event_value) AS revenue
FROM statnive.events_raw
WHERE is_bot = 0 AND (event_type = 'pageview' OR is_goal = 1)
GROUP BY site_id, hour;

ALTER TABLE statnive.mv_daily_pages{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY QUERY
SELECT
    site_id,
    toDate(time) AS day,
    pathname,
    count() AS views,
    uniqCombined64State(visitor_hash) AS visitors_state,
    sum(is_goal) AS goals,
    sum(event_value) AS revenue
FROM statnive.events_raw
WHERE is_bot = 0
GROUP BY site_id, day, pathname;

ALTER TABLE statnive.mv_daily_sources{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY QUERY
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
    sum(event_value) AS revenue
FROM statnive.events_raw
WHERE is_bot = 0
GROUP BY site_id, day, referrer_name, channel, utm_source, utm_medium, utm_campaign;
