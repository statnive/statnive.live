-- 021_sites_tz_default_utc.sql — flip statnive.sites.tz default from
-- 'Asia/Tehran' (migration 003) to 'UTC'. Affects only NEW rows created
-- after this migration runs. Existing rows keep their explicit tz value:
-- SamplePlatform (jurisdiction='IR') stays on 'Asia/Tehran'; Televika
-- (site_id=4) stays on whatever the admin set at onboarding (typically
-- 'Europe/Berlin').
--
-- Why: the salt-rotation path (internal/identity/salt.go) now reads
-- sites.tz instead of hardcoding "Asia/Tehran". UTC is the
-- regulator-safe default for new SaaS tenants — every other choice
-- would re-introduce a jurisdiction signal in the public source code.
-- Per-site overrides remain available via the admin UI.
--
-- Templated for single-node (current) and Distributed (v1.1+) using
-- the same Go text/template convention as migrations 001-020.
--
-- Idempotent: ClickHouse MODIFY COLUMN with the same default is a
-- metadata-only no-op when re-run.

ALTER TABLE statnive.sites{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    MODIFY COLUMN tz LowCardinality(String) DEFAULT 'UTC';
