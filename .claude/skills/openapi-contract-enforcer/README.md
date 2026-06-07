# openapi-contract-enforcer — full spec

## Rule

`statnive-live` ships a **drift-proof, live-verified OpenAPI 3.1 contract**
([`api/openapi.yaml`](../../../api/openapi.yaml)). It is a *hybrid* contract:

- **Routes are derived from code** — `cmd/specgen` walks the chi router (via the
  `internal/httpapi.BuildRouter` seam in SpecMode) and emits a deterministic
  skeleton (`api/openapi.gen.yaml`).
- **Semantics are hand-authored once** — `api/overlay.yaml` (+ `api/components/**`),
  the only hand-edited contract files.
- **The two are merged** by `cmd/specgen` (deep-merge) into the committed
  `api/openapi.yaml`. That file and the skeleton are GENERATED — never hand-edit
  them.

## How the gate works

| Gate | Command | Catches |
|---|---|---|
| Route coverage | `make test` (`internal/httpapi` tests) | a registered route missing from the spec (or an orphan) |
| Drift | `make spec-check` + `TestContractInSync` | overlay/router changed without `make spec-build` |
| Schema fidelity | `make test` (`TestOverlay_SchemasMatchGoStructs`) | a schema that doesn't match its Go struct |
| Lint | `make spec-lint` (redocly + spectral OWASP) | invalid spec; examples that don't validate; missing security |
| Breaking | `make spec-breaking` (oasdiff `--fail-on ERR`) | a backward-incompatible change vs `origin/main` |
| Types | `npm --prefix web run types:check` | SPA stats types out of sync with the contract |
| Contract fuzz | `make spec-fuzz` (Schemathesis, loopback) | runtime schema/status drift (read/public only) |
| Live | `prod-probe.sh` Prism proxy (deploy-gated) | the running server diverging from the spec |

CI runs all of these (`make test` in `build-test-lint`; the rest in the `spec`
job + the deploy-saas live step). The pre-push hook runs `spec-check` (fast,
Go-only) and the full `ci-local` runs `spec-lint` + `spec-breaking`.

## When it fires

See SKILL.md `## When this skill fires`. In short: any route mount, any
on-the-wire struct, the overlay, the non-chi surfaces, or the SPA api client.

## Research anchors

- jaan-to Self-Syncing API Contract standard (derive routes / author semantics /
  gate drift / verify live).
- Perfect API Contract guide — research 59 (3.1 contract generator), 83
  (Postman+OpenAPI pipeline), 84 (Swagger tooling: Scalar/Redoc, redocly,
  spectral, oasdiff, schemathesis).

## Relationship to other guardrails

- [`mcp-parity-enforcer`](../mcp-parity-enforcer/README.md) — a new dashboard
  read route fires both; add the MCP tool AND the OpenAPI operation.
- [`preact-signals-bundle-budget`](../preact-signals-bundle-budget/README.md) —
  the generated SPA types are type-only (0 runtime bytes).
- [`air-gap-validator`](../air-gap-validator/README.md) — the offline viewer is
  vendored + CSP-locked; `make docs-airgap-grep` + the offline Playwright proof.
