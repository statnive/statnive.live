-- 023_oauth.sql — OAuth 2.1 authorization-server state for the ChatGPT-app
-- onboarding path (PR-E). Three tables: registered clients, single-use
-- authorization codes, and rotating refresh tokens.
--
-- These tables are created in EVERY build (the migration runner has no build
-- tags), but only the `chatgpt_app` binary mounts handlers that read/write
-- them — see internal/oauthas/ (//go:build chatgpt_app) and the
-- cmd/statnive-live/oauthas.go / oauthas_stub.go mount pair. In the default +
-- air-gap/inside-iran binaries they stay empty and unreferenced (inert; an
-- empty ReplacingMergeTree costs ~nothing and air-gap-validator inspects the
-- Go build, not the CH schema).
--
-- Engine: ReplacingMergeTree(version). Reads via FINAL. All three tables are
-- tiny (one ChatGPT client; codes are 60s-TTL; refresh tokens bounded by
-- active sessions) — microseconds to scan. Single-use / rotation atomicity is
-- enforced PROCESS-SIDE by a mutex in internal/oauthas/store.go (the binary is
-- single-node per CLAUDE.md); these tables are the durable + audit record.
--
-- Privacy / security:
--   * NO raw secrets at rest — client_secret, authorization code, and refresh
--     token are each SHA-256-hashed (hex, FixedString(64)); the raw values
--     live only in the HTTP response and the client. Privacy Rule 3 (SHA-256+).
--   * user_id links a code/refresh-token to the consenting dashboard user →
--     DSAR erasure deletes WHERE user_id=? (internal/privacy; PR-E extends the
--     user-scoped eraser). site_ids is the consented (scope-clamped) set.
--   * code/token "consume"/"rotate"/"revoke" are higher-version upserts, never
--     DELETEs (audit trail; latest version wins on FINAL).
--
-- No inline trailing comments on column rows (migration 004/010 lesson — the
-- clickhouse-go prepared-statement path miscounts parens in "-- ...(..)" tails).
--
-- ORDER BY leads with the hash lookup key, NOT site_id: these are auth grant
-- rows, not analytics rows, so the tenancy choke point (Rule 8) does not apply
-- and tenancy-grep allow-lists them (same carve-out as user_sites/sessions).

CREATE TABLE IF NOT EXISTS statnive.oauth_clients{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    client_id          String,
    client_secret_hash FixedString(64),
    client_name        String,
    redirect_uris      Array(String),
    scopes             Array(String),
    created_at         DateTime('UTC') DEFAULT now(),
    updated_at         DateTime('UTC') DEFAULT now(),
    revoked            UInt8           DEFAULT 0,
    version            UInt64          DEFAULT toUnixTimestamp(now())
)
ENGINE = {{if .Cluster}}ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/oauth_clients', '{replica}', version){{else}}ReplacingMergeTree(version){{end}}
ORDER BY (client_id);

CREATE TABLE IF NOT EXISTS statnive.oauth_auth_codes{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    code_hash      FixedString(64),
    client_id      String,
    user_id        UUID,
    redirect_uri   String,
    code_challenge String,
    scope          String,
    audience       String,
    site_ids       Array(UInt32),
    issued_at      DateTime('UTC') DEFAULT now(),
    expires_at     DateTime('UTC'),
    consumed       UInt8           DEFAULT 0,
    version        UInt64          DEFAULT toUnixTimestamp(now())
)
ENGINE = {{if .Cluster}}ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/oauth_auth_codes', '{replica}', version){{else}}ReplacingMergeTree(version){{end}}
ORDER BY (code_hash)
TTL expires_at + INTERVAL 1 DAY DELETE;

CREATE TABLE IF NOT EXISTS statnive.oauth_refresh_tokens{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    token_hash     FixedString(64),
    family_id      UUID,
    client_id      String,
    user_id        UUID,
    scope          String,
    audience       String,
    site_ids       Array(UInt32),
    issued_at      DateTime('UTC') DEFAULT now(),
    expires_at     DateTime('UTC'),
    rotated        UInt8           DEFAULT 0,
    family_revoked UInt8           DEFAULT 0,
    version        UInt64          DEFAULT toUnixTimestamp(now())
)
ENGINE = {{if .Cluster}}ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/oauth_refresh_tokens', '{replica}', version){{else}}ReplacingMergeTree(version){{end}}
ORDER BY (token_hash)
TTL expires_at + INTERVAL 30 DAY DELETE;
