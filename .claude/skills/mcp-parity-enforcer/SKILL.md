---
name: mcp-parity-enforcer
description: MUST USE when adding or changing a read surface — a `storage.Store` read method, an off-interface concrete read (e.g. `*ClickHouseStore.EventNameCardinality`), a dashboard/admin GET route, a new `daily_*` rollup migration, or a new dashboard panel/report. Enforces the no-gap rule — every read surface ships its MCP tool + `internal/mcp/catalog.go` row + `parity_test.go` coverage entry in the SAME PR, so `make mcp-parity` stays green. Rejects new reads that have no MCP coverage.
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0"
  phase: 2
  research: "PLAN.md (v2 MCP agent surface) §No-gap governance; jaan-to/docs/research/82 (MCP context discipline), 27 (analytics-MCP positioning)"
---

# mcp-parity-enforcer

> **The no-gap rule (PLAN.md §No-gap governance):** *no read feature or report — present or future — may exist without an MCP tool, and every version bumps the MCP in lockstep.* This skill is the human-facing half; `make mcp-parity` (the Go test in `internal/mcp/parity_test.go`) is the CI-blocking half.

A read surface that the dashboard can show but the MCP can't answer is a silent gap: an operator asking the agent "what's site X's consent mode?" or "how did organic convert?" gets nothing, even though the data exists. The gate makes that gap **fail the build** instead of shipping quietly.

## When this skill fires

Any PR that touches:

- `internal/storage/store.go` — a new method on the `Store` interface.
- `internal/storage/*.go` — a new **concrete** read method on `*ClickHouseStore` (off-interface, like `EventNameCardinality`). Reflection over the interface can't see these — they must be pinned in `offInterfaceCoverage`.
- `internal/dashboard/router.go`, `internal/admin/router.go`, `cmd/statnive-live/main.go` — a new mounted **GET** route.
- `clickhouse/` migrations adding a `daily_*` rollup (a future panel's backing data).
- A new dashboard panel / report in `web/` whose data is a new read.

## What to do when it fires

1. **Add the MCP tool.** Create the tool in `internal/mcp/tools.go` (handler + input/output schema) and register it in `catalog()` in `internal/mcp/tools.go`. Match the existing pattern: `RoleClass` (`api` for analytics, `admin` for operator/config reads), `Scoped` (true for per-site reads), `readOnly()` annotations, and route all output through the `marshalResult` sanitize choke point (automatic via the dispatcher).
2. **Add the parity coverage entry** in `internal/mcp/parity_test.go`:
   - interface method → add to `storeMethodCoverage` (`"NewMethod": "new_tool"`, or `"exclude: <reason>"`).
   - off-interface concrete read → add to `offInterfaceCoverage`.
3. **Add the security tests** (every MCP PR ships them — see the existing `pr2b_security_test.go` / `pr3_test.go`): cross-tenant `-32602`, role floor (if admin), output sanitization (if the result carries UGC strings), budget applies, and a no-PII assertion.
4. **Add the integration test** in `test/mcp_integration_test.go` if the read hits ClickHouse (CH-oracle: MCP output == direct `Store`/raw query).
5. **Run the gate:** `make mcp-parity` (fast) then `make test` + `make test-integration`. Green = the gap is closed.

## Allowed exclusions (must be reasoned, in `parity_test.go`)

A read may be excluded from a tool only for a documented reason — mirror the PLAN.md coverage table:

- **PII** — surfaces carrying email / user_id / raw IP (e.g. `GET /api/admin/users`). `my_access` covers the non-PII "what can I see?" question.
- **Not Q&A data** — Prometheus `/metrics` (scrape format), static reference lists (`/api/admin/{currencies,timezones}`), UI-state flags, static content (legal pages, tracker.js).
- **Write surface** — `POST`/`PATCH` routes are out by definition (reads on the same path are covered by `site_config` / `goals_list`).
- **Pending** — a future read whose backing rollup/endpoint isn't built yet (`exclude: pending daily_devices`); flip to a tool the moment the backing code lands.

## What this skill does NOT do

- It does not relax the read-only invariant — never propose a write/mutation tool.
- It does not add an MCP tool for a PII or write surface — those are exclusions.
- It does not bypass the four authz gates (role floor → grant hydration → `ActorCanReadSite` → SQL choke point) or the sanitize choke point.

## Verification

`make mcp-parity` is green AND the new tool has cross-tenant + (role-floor if admin) + sanitize + budget + no-PII tests AND (if CH-backed) a CH-oracle integration test. The PR-template line — *"☐ Adds/changes a read surface? → MCP tool added, catalog updated, `make mcp-parity` green"* — must be checked.
