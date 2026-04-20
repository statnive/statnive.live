---
name: clickhouse-upgrade-playbook
description: >
  Advisory runbook for migrating ClickHouse from single-node MergeTree to replicated
  cluster. Fires when `migrations/*.sql` or `migrations/**/*.tmpl` contain `Engine=`
  or `{{if .Cluster}}`. Routes the agent to the hard-link `ATTACH PARTITION` procedure,
  Keeper ensemble setup, and the data-migration caveat that `{{if .Cluster}}` handles
  DDL templating but NOT data movement. No Semgrep rules — advisory only. Use
  alongside `clickhouse-operations-review` (enforcement) and paired
  `clickhouse-cluster-migration` skill (templating lint).
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 8
  research: "jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md §Gap 3 (lines 598, 711–728)"
  companion: clickhouse-operations-review
---

# clickhouse-upgrade-playbook

Reference-only runbook for the single-node → replicated-cluster migration at P5 (SaaS scale). **No Semgrep rules** — this skill advises, it doesn't gate. The paired `clickhouse-cluster-migration` skill handles the `{{if .Cluster}}` templating lint.

## When this skill fires

Any migration file (`migrations/*.sql`, `migrations/**/*.tmpl`) containing:
- `Engine=` (any value)
- `{{if .Cluster}}`

The skill surfaces this runbook to prevent an agent from believing the config flip from `Cluster=false` to `Cluster=true` constitutes "cluster upgrade automation". It is not.

## Four hard constraints (doc 28 line 728)

### 1. `ALTER TABLE MODIFY ENGINE` does NOT convert MergeTree → ReplicatedMergeTree

CH issues [#70118](https://github.com/ClickHouse/ClickHouse/issues/70118) and [#14576](https://github.com/ClickHouse/ClickHouse/issues/14576) confirm this is unsupported. Any migration authored as `ALTER TABLE events MODIFY ENGINE = ReplicatedMergeTree(...)` will fail at runtime. Do not ship it.

### 2. Preferred path — hard-link `ATTACH PARTITION`

Near-zero disk cost via immutable inode hard links. Procedure:

```sql
-- Step 1: Create the replicated-clone table with a distinct name
CREATE TABLE events_replicated ON CLUSTER '{cluster}' AS events
ENGINE = ReplicatedMergeTree(
  '/clickhouse/tables/{shard}/events_replicated',
  '{replica}'
)
ORDER BY (site_id, event_time);

-- Step 2: Freeze merges on source
SYSTEM STOP MERGES events;

-- Step 3: Hard-link every partition
-- (script enumerates system.parts active partitions)
ALTER TABLE events_replicated ATTACH PARTITION '202604' FROM events;
ALTER TABLE events_replicated ATTACH PARTITION '202603' FROM events;
-- ... for every partition

-- Step 4: Atomic rename
RENAME TABLE
  events TO events_mergetree_backup,
  events_replicated TO events;

-- Step 5: Drop the old backup after 24h soak
DROP TABLE events_mergetree_backup;

-- Step 6: Re-enable merges
SYSTEM START MERGES events;
```

**Critical:** Keep `events_mergetree_backup` for 24h as a rollback safety net. Only drop after operations confirm parity.

### 3. `convert_to_replicated` flag file (CH ≥ 23.8)

Alternative path on recent CH:

```bash
# On the source host, before restart
touch /var/lib/clickhouse/data/default/events/convert_to_replicated

# Restart CH
systemctl restart clickhouse-server

# The table is now a ReplicatedMergeTree with the same data in place.
# ZooKeeper / Keeper path is auto-generated from table UUID.
```

**Constraint:** Requires CH ≥ 23.8 AND Keeper / ZooKeeper already running. Works table-by-table, not cluster-wide.

### 4. Keeper 3-node RAFT over ZooKeeper

Ports: 9181 client / 9234 raft. Dedicated hosts recommended over embedded Keeper (embedded shares fate with CH). NuRaft implementation built into clickhouse-keeper binary.

Minimum deployment:

```xml
<!-- /etc/clickhouse-keeper/keeper_config.xml -->
<clickhouse>
  <keeper_server>
    <tcp_port>9181</tcp_port>
    <server_id>1</server_id>
    <coordination_settings>
      <operation_timeout_ms>10000</operation_timeout_ms>
      <session_timeout_ms>30000</session_timeout_ms>
      <raft_logs_level>information</raft_logs_level>
    </coordination_settings>
    <raft_configuration>
      <server>
        <id>1</id>
        <hostname>keeper-1.internal</hostname>
        <port>9234</port>
      </server>
      <server>
        <id>2</id>
        <hostname>keeper-2.internal</hostname>
        <port>9234</port>
      </server>
      <server>
        <id>3</id>
        <hostname>keeper-3.internal</hostname>
        <port>9234</port>
      </server>
    </raft_configuration>
  </keeper_server>
</clickhouse>
```

## `{{if .Cluster}}` scope (important — read carefully)

The Go-template `{{if .Cluster}}` pattern authored in every migration since doc 24 §Sec 2 Migration 0029 handles **DDL templating only**. It emits:

- `CREATE TABLE ... ON CLUSTER '{{.ClusterName}}'` vs `CREATE TABLE ...`
- `Engine = ReplicatedMergeTree(...)` vs `Engine = MergeTree()`
- `Engine = Distributed(...)` overlay vs none

It does **NOT** move data. When you flip `Cluster=true` for the first time:
- Fresh tables created by subsequent migrations are Replicated by default.
- Existing MergeTree tables stay MergeTree — you must run the hard-link ATTACH procedure above, or the `convert_to_replicated` flag file procedure, manually.

The paired `clickhouse-cluster-migration` skill lints the templating syntax itself. This skill (advisory) reminds the agent that templating ≠ migration.

## Data-migration checklist (before running either path)

- [ ] Keeper 3-node RAFT ensemble is quorate (`mntr` → `zk_server_state=leader/follower`, `zk_synced_followers=2`)
- [ ] `SYSTEM DROP DNS CACHE` + `SYSTEM DROP MARK CACHE` on all CH nodes (avoid stale routing)
- [ ] Storage has 2× current data size free (hard links are cheap but CH temporarily double-bookkeeps during ATTACH)
- [ ] Read-only window scheduled (hard-link ATTACH is <1s per partition but rename is atomic only within a single instance)
- [ ] Rollback plan tested in staging: `RENAME` back to `events_mergetree_backup`
- [ ] `clickhouse-backup` snapshot taken immediately before the migration starts
- [ ] Row-count parity assertion script ready to run post-migration
- [ ] `uniqCombined64Merge` mergeability assertion ready to run post-migration

## Post-migration validation

```sql
-- Row-count parity
SELECT 'events_mergetree_backup' AS src, sum(rows) FROM system.parts
  WHERE table = 'events_mergetree_backup' AND active
UNION ALL
SELECT 'events' AS src, sum(rows) FROM system.parts
  WHERE table = 'events' AND active;

-- Rollup integrity
SELECT uniqCombined64Merge(uniq_state) FROM rollup_daily FINAL FORMAT Null;

-- Replication health
SELECT database, table, is_leader, total_replicas, active_replicas
FROM system.replicas
WHERE table = 'events';
```

Expected: row counts equal, uniqCombined64Merge returns without error, `total_replicas = active_replicas`.

## Anti-patterns

- **`INSERT INTO SELECT` for >10M-row tables.** Rebuilds merge history, kills disk, slow. Only acceptable for small reference tables.
- **Treating `{{if .Cluster}}` as automation.** It is DDL templating. Data movement is manual.
- **Running `ATTACH PARTITION` without `SYSTEM STOP MERGES` first.** Race condition: a merge can consume the source partition mid-attach.
- **Embedded Keeper on CH nodes for production cluster.** Works for dev; shares fate with CH OOM. Dedicated hosts at P5.
- **ZooKeeper instead of Keeper.** Doc 28 line 598 confirms Keeper replaces ZooKeeper at P5 — simpler ops, same protocol, built into clickhouse-keeper binary.

## Research anchor

Doc 28 §Gap 3 lines 598 (core constraints), 711–728 (skill spec body).