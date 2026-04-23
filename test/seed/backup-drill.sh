#!/usr/bin/env bash
# Seed ~10 K synthetic events across 2 site_ids + 4 paths so the
# materialized views have multi-group state to back up + restore.
#
# Env: CH_CLIENT, CH_HOST (127.0.0.1), CH_PORT (19000 per docker-compose.dev.yml).

set -euo pipefail

CH_CLIENT="${CH_CLIENT:-clickhouse-client}"
CH_HOST="${CH_HOST:-127.0.0.1}"
CH_PORT="${CH_PORT:-19000}"

ch() {
  "$CH_CLIENT" --host "$CH_HOST" --port "$CH_PORT" -q "$1"
}

# generateRandom-backed seed — cheap and schema-agnostic for the columns
# we don't exercise downstream. Rows are bucketed into two tenants and
# 4 paths so the rollups have multi-group state to aggregate.
ch "
INSERT INTO statnive.events_raw
  (site_id, time, visitor_hash, hostname, pathname, channel, country_code, event_type, event_name)
SELECT
  1 + (number % 2)                              AS site_id,
  now() - toIntervalSecond(number % 3600)       AS time,
  reinterpretAsFixedString(cityHash64(number))  AS visitor_hash,
  'seed.example'                                AS hostname,
  ['/', '/about', '/pricing', '/blog'][1 + (number % 4)] AS pathname,
  ['direct', 'organic', 'social', 'referral'][1 + (number % 4)] AS channel,
  'IR'                                          AS country_code,
  'pageview'                                    AS event_type,
  'pageview'                                    AS event_name
FROM numbers(10000)
"

# Nudge any pending async-insert buffers to flush so the MV state files
# exist on disk before clickhouse-backup snapshots the data directory.
ch "SYSTEM FLUSH ASYNC INSERT QUEUE" || true
ch "SYSTEM FLUSH LOGS"               || true

# Report the final state so the calling job can log it.
ch "SELECT 'events_raw', count() FROM statnive.events_raw UNION ALL
    SELECT 'hourly_visitors', count() FROM statnive.hourly_visitors UNION ALL
    SELECT 'daily_pages', count() FROM statnive.daily_pages UNION ALL
    SELECT 'daily_sources', count() FROM statnive.daily_sources
    FORMAT TSV"
