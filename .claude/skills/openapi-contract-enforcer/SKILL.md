---
name: openapi-contract-enforcer
description: MUST USE when adding or changing an HTTP route or a documented response/request shape — a chi route mount (`internal/httpapi/router.go`, `internal/{dashboard,admin}/router.go`, `internal/dashboard/mcp_tokens.go`, `cmd/statnive-live/main.go`), a response struct (`internal/storage/result.go`, `internal/auth/handlers.go`), the ingest `RawEvent` (`internal/ingest/event.go`), the non-chi surfaces (`cmd/statnive-live/mcp.go`, `oauthas.go`), the overlay (`api/overlay.yaml`), or the SPA api client (`web/src/api/*`). Enforces the drift-proof contract — every route is in `api/openapi.yaml` and every response schema matches its Go struct, regenerated via `make spec-build`. Rejects routes/shapes that drift from the committed contract.
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0"
  phase: 2
  research: "jaan-to Self-Syncing API Contract standard; Perfect API Contract guide (research 59/83/84)"
---

# openapi-contract-enforcer

> **The drift-proof rule:** *every HTTP route the chi router registers appears in `api/openapi.yaml`, every documented response schema matches its Go struct field-for-field, and `api/openapi.yaml` is GENERATED (never hand-edited) by merging `api/overlay.yaml` over the router-walked skeleton.* This skill is the human-facing half; the Go tests in `internal/httpapi` + `internal/specgen` and `make spec-check` / `spec-lint` / `spec-breaking` are the CI-blocking half.

## When this skill fires

Editing any of:

- **Routes** — `internal/httpapi/router.go`, `internal/dashboard/router.go`, `internal/admin/router.go`, `internal/dashboard/mcp_tokens.go`, or a `router.Method`/`Mount` in `cmd/statnive-live/main.go`.
- **Response / request shapes** — `internal/storage/result.go`, `internal/auth/handlers.go`, `internal/ingest/event.go`, or another struct surfaced on the wire.
- **Non-chi surfaces** — `cmd/statnive-live/mcp.go` (MCP-over-HTTP), `cmd/statnive-live/oauthas.go` (OAuth-AS). These are invisible to `chi.Walk`, so they are hand-listed in `internal/httpapi/coverage_test.go` (`nonChiRoutes`) AND in `api/overlay.yaml`.
- **The contract** — `api/overlay.yaml` or `api/components/**` (the ONLY hand-edited contract files).
- **The SPA client** — `web/src/api/*` (stats types are re-exported from the generated `web/src/api/generated.ts`).

## What to do (the workflow)

1. Make the route / struct change.
2. Update `api/overlay.yaml` (+ `api/components/**`) with the operation's semantics + a field-accurate schema. **Never hand-edit `api/openapi.yaml` or `api/openapi.gen.yaml`** — they are generated.
3. Regenerate: `make spec-build` (walks the router → skeleton, deep-merges the overlay → `api/openapi.yaml`).
4. If a response type changed: `npm --prefix web run types:gen` (regenerates `web/src/api/generated.ts`).
5. Gate green before commit:
   - `make spec-check` — committed contract matches a fresh regen (drift gate).
   - `make spec-lint` — redocly + spectral (OWASP) clean; examples validate.
   - `make spec-breaking` — no backward-incompatible change vs `origin/main` (or an intentional, reviewed one).
   - `make test` — `TestSpec_EveryRouteDocumented` / `NoOrphanSpecPaths` / `NonChiSurfacesDocumented` / `TestOverlay_SchemasMatchGoStructs` / `TestContractInSync`.
   - `npm --prefix web run types:check` + `make bundle-gate` — SPA types in sync, 0 runtime bytes.

A new route with no overlay operation fails `make test` + `make spec-lint`; a stale `api/openapi.yaml` fails `make spec-check` + `TestContractInSync`. That is the "go document me" signal.

## Non-negotiables

- `api/openapi.yaml` + `api/openapi.gen.yaml` are GENERATED — a repo-local `.claude/settings.json` PreToolUse hook blocks hand-edits; CI `make spec-check` is the hard gate.
- Document **reality**, not the ideal: snake_case JSON, the `{"error":"..."}` envelope (some endpoints emit `text/plain`), bare `202`/`204` on `/api/event`, Prometheus text on `/metrics`, 501 on feature-flagged stats. Don't claim shapes the server doesn't emit.
- Schemas are field-accurate: `TestOverlay_SchemasMatchGoStructs` reflects the Go structs against the overlay (catches the `SEORow`-has-no-`rpv` / `drop_off_pct` class).
- Examples use real fixture data and must validate against their schema (`no-invalid-*-examples`).

## Relationship to other guardrails

- **`mcp-parity-enforcer`** — overlapping triggers on `internal/{dashboard,admin}/router.go` + `internal/storage/`. A new dashboard read route fires BOTH: it needs an MCP tool (mcp-parity) AND an OpenAPI operation (this skill). Complementary, not redundant — run both gates.
- **`preact-signals-bundle-budget`** — overlapping trigger on `web/src/api/*`. The generated types are type-only (0 runtime bytes); `make bundle-gate` stays green. Don't add a runtime client to the SPA.
- **`air-gap-validator`** — the offline API viewer (`docs/api/`) is vendored + CSP-locked; `make docs-airgap-grep` + the offline Playwright proof enforce zero outbound.
