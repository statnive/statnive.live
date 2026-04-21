# Enforcement tests (reference)

> The integration tests that pin the invariants in [CLAUDE.md](../../CLAUDE.md). Phase 0 / Phase 7 deliverables. `/simplify` and PR review reject regressions against this list on day one.

| Test | File | Asserts |
|---|---|---|
| Enrichment order | [`test/integration/enrichment_order_test.go`](../../test/integration/enrichment_order_test.go) | Pipeline order is `identity → bloom → geo → ua → bot → channel` (Architecture Rule 6). |
| Air-gap | [`test/integration/airgap_test.go`](../../test/integration/airgap_test.go) | Runs the binary under `iptables -A OUTPUT -j DROP`; ingest, rollup materialization, and dashboard all work with zero required outbound. |
| Multi-tenant isolation | [`test/integration/multitenant_test.go`](../../test/integration/multitenant_test.go) | Every dashboard query includes `WHERE site_id = ?`; no cross-tenant row leaks (Project Goal 4). |
| PII leak | [`test/integration/pii_leak_test.go`](../../test/integration/pii_leak_test.go) | Raw IP and raw `user_id` never appear in ClickHouse tables or in the JSONL audit log (Privacy Rules 1, 4). |
| AGPL-free | [`test/security/no_agpl_test.go`](../../test/security/no_agpl_test.go) | `go-licenses` reports every direct + transitive dep as MIT / Apache / BSD / ISC (License Rules). |
| Frontend tenancy | [`web/src/__tests__/tenant-isolation.test.tsx`](../../web/src/__tests__/tenant-isolation.test.tsx) | Preact signal stores don't leak `site_id` state across dashboard views. |

## Running

```bash
make test                 # unit (fast)
make test-integration     # needs docker compose up clickhouse
make test-security        # go-licenses gate
npm --prefix web run test # Vitest
```

## Project-local SKILL.md guardrails

14 scaffolded skills under [`.claude/skills/`](../../.claude/skills/) encode Architecture Rules 2/5/8 + Isolation + Privacy Rules 2/3/4 + Iranian-DC operational contract (doc 28) + GeoIP privacy (doc 28) + ClickHouse ops (doc 28) as triggerable guardrails. Full specs in each skill's `README.md`; trigger mapping in [CLAUDE.md § Dev Tooling](../../CLAUDE.md#dev-tooling). Semgrep bodies + fixtures fill in per phase — doc-25 set shipped, doc-27 set mid-implementation, doc-28 set scaffolded for Weeks 17–22.

`/simplify` and PR review reject:
- Any new unguarded query (no `WHERE site_id = ?`).
- Any new dependency without a license check.
- Any new outbound network call not behind a config flag.
- Any new `Nullable(...)` column without a `-- NULLABLE-OK:` justification.
