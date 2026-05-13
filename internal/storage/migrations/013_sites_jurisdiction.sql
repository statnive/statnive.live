-- 013_sites_jurisdiction.sql — per-site jurisdiction + consent mode.
--
-- Cross-cutting Stage-3 migration. Adds three columns to statnive.sites:
--
--   jurisdiction     — operator's audience-targeting choice. Enum-shaped
--                      LowCardinality(String); valid values:
--                        DE | FR | IT | ES | NL | BE | IE | UK
--                        OTHER-EU | OTHER-NON-EU | IR
--                      Validation lives in Go (sites.SitePolicy.Validate);
--                      ClickHouse is permissive so a future locale can be
--                      added without a schema change.
--
--   consent_mode     — the four-value enum that drives the PolicyToMode
--                      switch: permissive | consent-free | consent-required
--                      | hybrid. NULL-equivalent default 'permissive'
--                      preserves the 3 live operators' current behaviour.
--
--   event_allowlist  — JSON-encoded list of event_name values the ingest
--                      gate accepts when consent_mode = consent-free or
--                      = hybrid (pre-consent half). Empty list means no
--                      cap. Stored as String, not Array(String), so a
--                      mode that doesn't enforce it doesn't pay the
--                      array-decoding cost on every lookup.
--
-- Existing-site backfill. The 3 production operators (statnive.com,
-- statnive.de, fr.statnive.com) are German/global — NOT Iran-targeted.
-- Backfilling to OTHER-NON-EU + permissive keeps their cookie posture,
-- their RespectGPC / RespectDNT settings, and their event_name behaviour
-- byte-for-byte identical until an operator consciously flips
-- jurisdiction or consent_mode via the admin UI (Stage 3 §Compliance card).
--
-- Hybrid is opt-in only: the backfill NEVER auto-applies it. New sites
-- created post-Stage-3 get jurisdiction-derived defaults at the Go layer
-- (sites.CreateSite); this migration only widens the columns.

ALTER TABLE statnive.sites{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS jurisdiction    LowCardinality(String) DEFAULT 'OTHER-NON-EU',
    ADD COLUMN IF NOT EXISTS consent_mode    LowCardinality(String) DEFAULT 'permissive',
    ADD COLUMN IF NOT EXISTS event_allowlist String                  DEFAULT '[]';

ALTER TABLE statnive.sites{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    UPDATE jurisdiction = 'OTHER-NON-EU',
           consent_mode = 'permissive',
           event_allowlist = '[]'
    WHERE jurisdiction = '' OR consent_mode = '';
