-- 015_rollup_channel.sql — propagate channel into hourly_visitors and
-- daily_pages so /api/stats/{overview,trend,pages,realtime} can honour
-- the channel filter. daily_sources already carries channel since
-- migration 002 — no change there.
--
-- Recipe matches migration 008 (MODIFY QUERY proven safe on these MVs):
-- ALTER TABLE … ADD COLUMN + ALTER TABLE … MODIFY ORDER BY (append-only
-- — CH disallows mid-tuple insertion on AggregatingMergeTree) +
-- ALTER TABLE mv_… MODIFY QUERY. Engine + ON CLUSTER templated for the
-- same single-node → Distributed migration story as 001/002/003/006/
-- 007/008/011.
--
-- MODIFY ORDER BY is metadata-only on AggregatingMergeTree — existing
-- parts retain their previous physical layout until natural background
-- compaction rewrites them. No synchronous re-merge at migration time,
-- so the binary boot path stays fast. First use of this op in the
-- project; migration 008 was MODIFY QUERY only (no ORDER BY change).
--
-- Historical rows inherit channel='' from the column DEFAULT.
-- Channel-filtered Overview / Pages / Realtime queries therefore return
-- only data ingested AFTER this migration runs. To backfill historical
-- channel attribution, operators may replay events_raw via
--   INSERT INTO statnive.hourly_visitors SELECT … FROM events_raw …
-- (out of scope here — runbook step, not the migration's job).
--
-- Storage impact: hourly_visitors / daily_pages each gain a
-- LowCardinality(String) column with cardinality ≤7 (canonical channels:
-- Direct / Organic Search / Social Media / Email / Referral / AI / Paid).
-- On the 100–200 M events/day design ceiling, the multiplier stays
-- inside the 8c/32GB envelope.

ALTER TABLE statnive.hourly_visitors{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS channel LowCardinality(String) DEFAULT '';

ALTER TABLE statnive.hourly_visitors{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY ORDER BY (site_id, hour, channel);

ALTER TABLE statnive.mv_hourly_visitors{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY QUERY
SELECT
    site_id,
    toStartOfHour(time) AS hour,
    channel,
    count() AS pageviews,
    uniqCombined64State(visitor_hash) AS visitors_state,
    sum(is_goal) AS goals,
    sum(event_value) AS revenue
FROM statnive.events_raw
WHERE is_bot = 0 AND (event_type = 'pageview' OR is_goal = 1)
GROUP BY site_id, hour, channel;

ALTER TABLE statnive.daily_pages{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS channel LowCardinality(String) DEFAULT '';

ALTER TABLE statnive.daily_pages{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY ORDER BY (site_id, day, pathname, channel);

ALTER TABLE statnive.mv_daily_pages{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY QUERY
SELECT
    site_id,
    toDate(time) AS day,
    pathname,
    channel,
    count() AS views,
    uniqCombined64State(visitor_hash) AS visitors_state,
    sum(is_goal) AS goals,
    sum(event_value) AS revenue
FROM statnive.events_raw
WHERE is_bot = 0
GROUP BY site_id, day, pathname, channel;
