-- 004_auth_schema.sql — users + sessions tables for Phase 2b auth/RBAC.
-- Engine: ReplacingMergeTree (in-cluster: ReplicatedReplacingMergeTree) —
-- low-write OLTP-ish tables where the newest row by updated_at wins on merge.
-- Rule 1 (raw = write-only) scopes to analytics; these are metadata.
-- Rule 5 (no Nullable) upheld via typed defaults.
-- Rule 8 (tenancy leads ORDER BY) upheld — every ORDER BY starts with site_id.
-- Privacy Rule 1 — no raw IP column (ip_hash is BLAKE3-128 via internal/identity).
-- Privacy Rule 3 — password_hash is bcrypt, not SHA-1/MD5.
--
-- Columns without inline trailing comments by convention — the migration
-- runner's statement splitter strips full-line "--" comments but leaves
-- inline comments intact, and the clickhouse-go prepared-statement path
-- miscounts parens in "-- ...(..)" tails.
--
-- users.password_hash is bcrypt cost 12 (see internal/auth/password.go).
-- sessions.session_id_hash is SHA-256 of the raw cookie; raw never stored.
-- sessions.ip_hash is BLAKE3-128 via internal/identity.
-- Both Enum8 role columns mirror internal/auth.Role: admin=1, viewer=2, api=3.

CREATE TABLE IF NOT EXISTS statnive.users{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    user_id       UUID,
    site_id       UInt32,
    email         String,
    username      String,
    password_hash String,
    role          Enum8('admin' = 1, 'viewer' = 2, 'api' = 3),
    disabled      UInt8           DEFAULT 0,
    created_at    DateTime('UTC') DEFAULT now(),
    updated_at    DateTime('UTC') DEFAULT now(),
    INDEX users_email_bf (email) TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = {{if .Cluster}}ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/users', '{replica}', updated_at){{else}}ReplacingMergeTree(updated_at){{end}}
ORDER BY (site_id, email);

CREATE TABLE IF NOT EXISTS statnive.sessions{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    session_id_hash FixedString(32),
    user_id         UUID,
    site_id         UInt32,
    role            Enum8('admin' = 1, 'viewer' = 2, 'api' = 3),
    created_at      DateTime('UTC') DEFAULT now(),
    last_used_at    DateTime('UTC') DEFAULT now(),
    expires_at      DateTime('UTC'),
    revoked_at      DateTime('UTC') DEFAULT toDateTime(0),
    updated_at      DateTime('UTC') DEFAULT now(),
    ip_hash         FixedString(16),
    user_agent      String
)
ENGINE = {{if .Cluster}}ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/sessions', '{replica}', updated_at){{else}}ReplacingMergeTree(updated_at){{end}}
ORDER BY (site_id, session_id_hash)
TTL expires_at + INTERVAL 7 DAY DELETE;
