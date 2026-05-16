-- 016_rollup_utm_content_term.sql — extend daily_sources with utm_content
-- and utm_term so the Campaigns panel can render the full UTM tuple
-- (source / medium / campaign / content / term) and group hierarchically.
-- events_raw already carries both columns (migration 001); only the
-- daily_sources rollup + its MV were missing them.
--
-- Cardinality-driven sort-key choice — matches the existing schema's
-- treatment of utm_campaign: high-cardinality UTM dims live in the MV's
-- GROUP BY only, NOT in daily_sources.ORDER BY. The ORDER BY stays
-- (site_id, day, channel, referrer_name, utm_source, utm_medium) — these
-- are the low-cardinality LowCardinality(String) dims used to prune
-- partitions. utm_content + utm_term + utm_campaign are unbounded per
-- tenant (one tracker tag is "TLVK-WTraffic-S-CompetitorsBrands-EP-July25-
-- 2021-MD"-shaped) so keeping them out of the sort key avoids primary-
-- index bloat at the 100–200M events/day design ceiling.
--
-- Type choice — plain String, no LowCardinality and no DEFAULT. Mirrors
-- the existing utm_campaign column. LowCardinality(String) zero value is
-- the empty string, but on high-cardinality columns the dictionary
-- overhead negates the benefit; String zero value is also '', so
-- historical rows read as '' until forward rows accrue (same backfill
-- story as migration 015 for channel).
--
-- MV recipe — ALTER TABLE mv_… MODIFY QUERY, exactly as migrations 008
-- and 015 did. CH treats this as metadata-only on the MV definition;
-- daily_sources receives the two new columns from the next MV-emitted
-- batch onward. Engine + ON CLUSTER templated for the single-node →
-- Distributed migration story (matches 001/002/003/006/007/008/011/015).
--
-- Backfill — historical daily_sources rows show utm_content='' and
-- utm_term='' until natural turnover. Operators wanting the back-fill
-- may replay events_raw via
--   INSERT INTO statnive.daily_sources SELECT … FROM events_raw …
-- (out of scope here — runbook step, not the migration's job).

ALTER TABLE statnive.daily_sources{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS utm_content String,
    ADD COLUMN IF NOT EXISTS utm_term String;

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
    utm_content,
    utm_term,
    count() AS views,
    uniqCombined64State(visitor_hash) AS visitors_state,
    sum(is_goal) AS goals,
    sum(event_value) AS revenue
FROM statnive.events_raw
WHERE is_bot = 0
GROUP BY site_id, day, referrer_name, channel, utm_source, utm_medium, utm_campaign, utm_content, utm_term;
