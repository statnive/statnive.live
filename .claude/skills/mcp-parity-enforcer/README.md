# mcp-parity-enforcer — full spec

## Rule

Encodes **PLAN.md §No-gap governance** (v2 MCP agent surface): every read surface statnive-live exposes — present or future — must have a matching MCP tool, or a documented exclusion. The MCP server is only useful if an agent can answer *any* question the dashboard can; a read with no tool is a silent capability gap.

This skill is the human-facing trigger. The CI-blocking enforcement is the Go test in [`internal/mcp/parity_test.go`](../../../internal/mcp/parity_test.go), run by `make mcp-parity` (also folded into `make test` and the `ci.yml` `build-test-lint` job, and a `make release` step).

## How the gate works

`internal/mcp/parity_test.go` is reflection-based and fails by construction:

- `TestParity_EveryStoreMethodHasTool` — reflects over the `storage.Store` interface; every method must have a `storeMethodCoverage` entry pointing to a real catalog tool (or `"exclude: <reason>"`).
- `TestParity_OffInterfaceReadsCovered` — pins concrete reads reflection can't see (`EventNameCardinality → event_audit`).
- `TestParity_NoStaleCoverageEntries` — fails if a coverage entry names a method that no longer exists (catches renames/removals).
- `TestParity_CatalogIntegrity` — unique tool names, non-nil handlers, all read-only.

A new `Store` read method (or a renamed one) with no coverage entry → `make mcp-parity` red → CI red.

## When it fires

See SKILL.md "When this skill fires". In short: changes to `internal/storage/store.go`, concrete `internal/storage/*.go` read methods, `internal/dashboard/router.go`, `internal/admin/router.go`, `cmd/statnive-live/main.go` route mounts, or new `daily_*` rollup migrations.

## Coverage of the four authz gates

A new tool inherits the dispatcher's uniform controls — the skill checklist just confirms they apply:

1. **Role floor** — `RoleClass` (`api` analytics / `admin` operator). api-role actors are `-32602` on admin tools (matches REST 403).
2. **Grant hydration** — multi-site session admins resolve via `LoadUserSites`.
3. **`ActorCanReadSite`** — cross-tenant → `-32602`, never empty rows.
4. **SQL choke point** — reads flow `whereTimeAndTenant` (no new SQL in `internal/mcp`).

Plus the **`marshalResult` sanitize choke point** (NFC + invisible-Unicode/HTML/secret stripping, recursive) on every output string, and the per-actor query **budget**.

## Research anchors

- PLAN.md (v2 MCP agent surface) — §Tool catalog, §No-gap governance, §Security/Permissions deep-review (agent F = completeness/no-gap auditor).
- `jaan-to/docs/research/82` — MCP context discipline (tool-token budget; skills generic, MCP provides real context).
- `jaan-to/docs/research/27` — analytics-MCP positioning.
- `jaan-to/docs/research/78` — MCP threat model (prompt-injection-via-tool-results; the sanitize choke point the new tool inherits).

## Relationship to other guardrails

- [`tenancy-choke-point-enforcer`](../tenancy-choke-point-enforcer/README.md) — the MCP adds no new SQL, so a new tool routes through the same `whereTimeAndTenant`; that skill still owns SQL.
- [`air-gap-validator`](../air-gap-validator/README.md) — a new tool must add no new dependency and no outbound call.
- This skill owns only the **read-surface ↔ MCP-tool** mapping.
