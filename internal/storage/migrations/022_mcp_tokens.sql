-- 022_mcp_tokens.sql — self-serve, dashboard-minted bearer tokens for the
-- read-only MCP HTTP transport (gap #1/#6/#8: token issuance + lifecycle +
-- secure handoff). A logged-in dashboard user mints a named, scoped,
-- expiring, revocable token; the raw value is shown once and never stored.
--
-- Only the SHA-256 hex of the raw token is persisted (token_hash_hex) —
-- same posture as auth.api_tokens config hashes. Scope is the set of
-- site_ids the minting user already had access to (clamped server-side at
-- mint time); the token can never read beyond it.
--
-- Engine: ReplacingMergeTree(version). version = unix-nanos at write time, so
-- the highest-version row for a token_id wins. Revoke = re-insert the same
-- row with revoked=1 + a higher version (no DELETE — audit trail; DSAR
-- erasure is a separate ALTER ... DELETE in internal/privacy). Bounded by
-- users x tokens (max-active-per-user cap) — microseconds to scan.
--
-- ORDER BY leads with token_hash_hex: the hot path is "given a bearer hash,
-- find the token" (LookupActive), so the primary-key range makes that a
-- bounded seek; token_id is the secondary key (revoke/list handle). This is
-- a grant-style table, NOT analytics — the tenancy choke point does not
-- apply (allow-listed, same as user_sites in migration 010).
--
-- No inline comments on column rows (migration 004 lesson — clickhouse-go
-- prepared-statement path miscounts parens in "-- ...(..)" tails).

CREATE TABLE IF NOT EXISTS statnive.mcp_tokens{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    token_id       UUID,
    user_id        UUID,
    token_hash_hex FixedString(64),
    name           String,
    site_ids       Array(UInt32),
    role           Enum8('admin' = 1, 'viewer' = 2, 'api' = 3),
    created_at     DateTime('UTC') DEFAULT now(),
    expires_at     DateTime('UTC') DEFAULT toDateTime(0, 'UTC'),
    last_used_at   DateTime('UTC') DEFAULT toDateTime(0, 'UTC'),
    revoked        UInt8           DEFAULT 0,
    version        UInt64
)
ENGINE = {{if .Cluster}}ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/mcp_tokens', '{replica}', version){{else}}ReplacingMergeTree(version){{end}}
ORDER BY (token_hash_hex, token_id);
