-- 001_initial.sql — schema_migrations + sites + events_raw.
-- Templated for single-node (current) and ReplicatedMergeTree + Distributed (SaaS scale)
-- via Go text/template; .Cluster is the cluster name or "" for single-node.
-- Rules: Architecture Rule 5 (no Nullable), Rule 8 (site_id leads ORDER BY),
-- Privacy Rule 1 (no raw IP column), PLAN.md:738 (DateTime not DateTime64).

CREATE DATABASE IF NOT EXISTS statnive{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}};

-- Migration tracking. Mirrors golang-migrate semantics: one row per applied
-- version, dirty flag for partial applies, sequence for ordering ties.
CREATE TABLE IF NOT EXISTS statnive.schema_migrations{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    version    UInt32,
    dirty      UInt8 DEFAULT 0,
    sequence   UInt32 DEFAULT 0,
    applied_at DateTime('UTC') DEFAULT now()
)
ENGINE = {{if .Cluster}}ReplicatedMergeTree('/clickhouse/tables/{shard}/schema_migrations', '{replica}'){{else}}MergeTree(){{end}}
ORDER BY version;

-- Site registry. Hostname → site_id resolution at ingest, plan gating later.
CREATE TABLE IF NOT EXISTS statnive.sites{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    site_id    UInt32,
    hostname   String,
    slug       String DEFAULT '',
    plan       LowCardinality(String) DEFAULT 'free',
    enabled    UInt8 DEFAULT 1,
    created_at DateTime('UTC') DEFAULT now()
)
ENGINE = {{if .Cluster}}ReplicatedMergeTree('/clickhouse/tables/{shard}/sites', '{replica}'){{else}}MergeTree(){{end}}
ORDER BY (site_id, hostname);

-- Raw event table. WRITE-ONLY (Architecture Rule 1). Dashboard reads from rollups.
-- 34 columns including site_id (PLAN.md:160). No raw IP — Privacy Rule 1.
CREATE TABLE IF NOT EXISTS statnive.events_raw{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}} (
    -- Tenancy + time (always first for index pruning)
    site_id          UInt32,
    time             DateTime('UTC') CODEC(Delta(4), ZSTD(1)),

    -- Identity (three layers, no Nullable — Privacy Rule 3 hashes only)
    user_id_hash     String DEFAULT '' CODEC(ZSTD(3)),
    cookie_id        String DEFAULT '' CODEC(ZSTD(3)),
    visitor_hash     FixedString(16) CODEC(ZSTD(1)),

    -- Page
    hostname         LowCardinality(String),
    pathname         String CODEC(ZSTD(3)),
    title            String DEFAULT '' CODEC(ZSTD(3)),

    -- Source
    referrer         String DEFAULT '' CODEC(ZSTD(3)),
    referrer_name    LowCardinality(String) DEFAULT '',
    channel          LowCardinality(String) DEFAULT '',
    utm_source       LowCardinality(String) DEFAULT '',
    utm_medium       LowCardinality(String) DEFAULT '',
    utm_campaign     String DEFAULT '' CODEC(ZSTD(3)),
    utm_content      String DEFAULT '' CODEC(ZSTD(3)),
    utm_term         String DEFAULT '' CODEC(ZSTD(3)),

    -- Audience
    province         LowCardinality(String) DEFAULT '',
    city             LowCardinality(String) DEFAULT '',
    country_code     FixedString(2) DEFAULT 'IR',
    isp              LowCardinality(String) DEFAULT '',
    carrier          LowCardinality(String) DEFAULT '',
    os               LowCardinality(String) DEFAULT '',
    browser          LowCardinality(String) DEFAULT '',
    device_type      LowCardinality(String) DEFAULT '',
    viewport_width   UInt16 DEFAULT 0,

    -- Event
    event_type       LowCardinality(String) DEFAULT 'pageview',
    event_name       LowCardinality(String) DEFAULT 'pageview',
    event_value      UInt64 DEFAULT 0,
    is_goal          UInt8 DEFAULT 0,
    is_new           UInt8 DEFAULT 0,
    prop_keys        Array(String) DEFAULT [],
    prop_vals        Array(String) DEFAULT [],

    -- User properties + meta
    user_segment     LowCardinality(String) DEFAULT '',
    is_bot           UInt8 DEFAULT 0
)
ENGINE = {{if .Cluster}}ReplicatedMergeTree('/clickhouse/tables/{shard}/events_raw', '{replica}'){{else}}MergeTree(){{end}}
PARTITION BY toYYYYMM(time)
ORDER BY (site_id, toDate(time), visitor_hash, time)
TTL time + INTERVAL 180 DAY DELETE
SETTINGS index_granularity = 8192;
