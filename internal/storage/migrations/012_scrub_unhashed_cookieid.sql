-- 012_scrub_unhashed_cookieid.sql — zero out raw UUID cookie_id values
-- left in events_raw before the hash-at-handler change. Rows written
-- after the new handler lands carry the "h:" prefix (HexCookieIDHash),
-- so the filter `cookie_id != '' AND cookie_id NOT LIKE 'h:%'` matches
-- only the legacy raw-UUID rows.
--
-- We zero out rather than re-hash because the master_secret is not
-- available to ClickHouse — re-hashing would require a Go-side
-- per-row backfill that's expensive and out of scope. The existing
-- 180-day TTL on events_raw clears these rows naturally; this mutation
-- accelerates compliance by stripping the raw UUIDs immediately.
--
-- No mutations_sync setting: the ALTER returns as soon as the mutation
-- is queued; ClickHouse rewrites affected parts in the background.
-- Blocking the migration runner here would stall every fresh deploy
-- behind a 10–60 min mutation on multi-GB legacy events_raw — the
-- 180-day TTL guarantees eventual clearance either way.
--
-- Idempotent: re-running matches zero rows because the previous run
-- already cleared them and new rows are hex-prefixed.

ALTER TABLE statnive.events_raw{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    UPDATE cookie_id = ''
    WHERE cookie_id != '' AND cookie_id NOT LIKE 'h:%';
