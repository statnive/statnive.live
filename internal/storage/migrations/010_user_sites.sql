-- 010_user_sites.sql — many-to-many user↔site grants with a per-site role.
--
-- Replaces the implicit 1-site-per-user model from migration 004 where
-- users.site_id was a single UInt32 baked into the user row. The new
-- table is the canonical authorization grant; users.site_id stays as a
-- legacy column for one release for fail-safe rollback (dropped in
-- migration 011 once v0.0.9 is fully ratified).
--
-- Engine: ReplacingMergeTree(updated_at). Reads via FINAL. The table is
-- bounded by users × enabled-sites, microseconds-to-scan on the SaaS
-- footprint. Revoke is an upsert with revoked=1 (latest updated_at wins).
-- No DELETEs — audit trail.
--
-- ORDER BY leads with user_id (the auth path is "given a user, what
-- sites?") and tenancy-grep allow-lists this table because grant rows
-- are not analytics rows; tenancy choke point applies to dashboard SQL.
--
-- No inline comments on column rows (migration 004 lesson — clickhouse-go
-- prepared-statement path miscounts parens in "-- ...(..)" tails).

CREATE TABLE IF NOT EXISTS statnive.user_sites{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    user_id    UUID,
    site_id    UInt32,
    role       Enum8('admin' = 1, 'viewer' = 2, 'api' = 3),
    created_at DateTime('UTC') DEFAULT now(),
    updated_at DateTime('UTC') DEFAULT now(),
    revoked    UInt8           DEFAULT 0
)
ENGINE = {{if .Cluster}}ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/user_sites', '{replica}', updated_at){{else}}ReplacingMergeTree(updated_at){{end}}
ORDER BY (user_id, site_id);

-- Backfill 1: every existing enabled user gets their legacy (site_id, role).
-- WHERE site_id > 0 guards against accidental zero-site rows. Safe on
-- rerun: ReplacingMergeTree collapses dup tuples (user_id, site_id).
INSERT INTO statnive.user_sites (user_id, site_id, role, created_at)
SELECT u.user_id, u.site_id, u.role, u.created_at
FROM   statnive.users AS u FINAL
WHERE  u.disabled = 0
  AND  u.site_id > 0;

-- Backfill 2 (operator bootstrap): grants the operator admin on every
-- currently-enabled site so they can manage all tenants from a single
-- account. Skipped when OperatorEmail is empty (dev/test/CI). Tuple-IN
-- guard makes the insert idempotent on rerun.
{{if .OperatorEmail}}INSERT INTO statnive.user_sites (user_id, site_id, role)
SELECT u.user_id, s.site_id, 'admin'
FROM   statnive.users AS u FINAL
CROSS JOIN statnive.sites AS s
WHERE  u.email = '{{.OperatorEmail}}'
  AND  u.disabled = 0
  AND  s.enabled = 1
  AND  (u.user_id, s.site_id) NOT IN (
      SELECT user_id, site_id
      FROM   statnive.user_sites FINAL
      WHERE  revoked = 0
  );{{end}}
