# Dev Tooling — Claude Code skills + MCP servers

Referenced from [`CLAUDE.md`](../CLAUDE.md) § Dev Tooling. This file holds the detail; CLAUDE.md holds the compact routing. When these two drift, this file wins.

All recommendations trace back to [`../../jaan-to/docs/research/23-ai-workflow-claude-skills-go-clickhouse-analytics.md`](../../jaan-to/docs/research/23-ai-workflow-claude-skills-go-clickhouse-analytics.md) (doc 23). We do **not** restate the research here — only decisions and deviations.

## Install summary

**4 skill collections installed (32 atomic skills total — 2 over doc 23 §Best Practices soft cap of 30; the `static-analysis` plugin nests 3 sub-skills, codeql/semgrep/sarif-parsing, all relevant to Phase 2):**

| Collection | Source | License | Skills installed | Phase coverage |
|---|---|---|---|---|
| cc-skills-golang | [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang) | MIT | 12 of 37 (curated) | 0, 1, 3, 6, 7 |
| clickhouse-agent-skills | [ClickHouse/agent-skills](https://github.com/ClickHouse/agent-skills) | Apache-2.0 | 6 of 6 (all) | 0, 1, 3, 6 |
| trailofbits-skills | [trailofbits/skills](https://github.com/trailofbits/skills) | CC-BY-SA-4.0 | 8 of 38 (curated) | 2 (security) |
| marina-skill | [The-Focus-AI/marina-skill](https://github.com/The-Focus-AI/marina-skill) | MIT | 4 of 4 (all) | 8 (deploy) |

**4 MCP servers configured** in [`.mcp.json`](../.mcp.json): `clickhouse` (Altinity), `gopls`, `hetzner`, `grafana`.

## Licensing decisions

CLAUDE.md § License Rules mandates MIT/Apache/BSD/ISC for anything **in the binary**. Skills and MCP servers are dev-time tooling — not bundled with the shipped binary — so that gate applies to the Go dependency tree, not to this directory. That said, each skill source was verified:

| Collection | License | Verdict | Rationale |
|---|---|---|---|
| cc-skills-golang | MIT | ✅ Install | Green across both bars. |
| clickhouse-agent-skills | Apache-2.0 | ✅ Install | Green across both bars. |
| marina-skill | MIT (declared in README, no LICENSE file) | ✅ Install | Declared MIT; safe for dev-time. |
| trailofbits-skills | CC-BY-SA-4.0 | ⚠️ Install unmodified | Dev-time documentation only. Share-alike applies to modifications, so we ship these verbatim. If we need to modify a ToB skill, fork & publish under CC-BY-SA. |
| darrenoakey/claude-skill-golang | **CC-BY-NC-4.0** | ❌ **Rejected** | Non-commercial only. statnive-live is sold commercially (SaaS + self-hosted license fee). Functionality overlap with cc-skills-golang (testing, linter) is substantial — no meaningful loss. |

Doc 23 originally recommended claude-skill-golang; we deviate because commercial-use is non-negotiable. CI gate enforcement (its main value-add) is handled by our own `.githooks/pre-commit` + `make lint` once scaffolded.

## Curated skill list

### cc-skills-golang (12 of 37)

The full repo ships 37 atomic skills. We install only the ones that map to statnive-live phases (Architecture Rule 7 — defer before building):

| Skill | Purpose |
|---|---|
| `golang-concurrency` | Goroutines, channels, errgroup, singleflight — Phase 1 ingestion pipeline |
| `golang-context` | Context propagation, cancellation, deadlines — all phases |
| `golang-database` | Connection pools, `database/sql` patterns — Phase 1, 3 |
| `golang-error-handling` | Error wrapping, sentinel errors, errors.Is/As — all phases |
| `golang-security` | Crypto, hashing, TLS, input validation — Phase 2 |
| `golang-performance` | Profiling, benchmarking, allocations — Phase 1, 7 |
| `golang-code-style` | Naming, formatting, idioms — all phases |
| `golang-cli` | Flag parsing, subcommands, exit codes — Phase 6 config |
| `golang-observability` | `slog`, metrics, traces — Phase 2 audit log, Phase 8 monitoring |
| `golang-project-layout` | Standard Go project layout — Phase 0 |
| `golang-linter` | golangci-lint, staticcheck config — Phase 7 gate |
| `golang-safety` | `unsafe` guidance, race conditions, memory safety — Phase 1, 2 |

**Skipped (no active phase yet):** grpc, samber-do/hot/lo/mo/ro/slog/oops, dependency-injection, dependency-management, design-patterns, documentation, data-structures, benchmark (covered by performance), modernize, naming (covered by code-style), popular-libraries, testing (pending testing framework selection in Phase 7), continuous-integration (our pre-commit hook + Makefile handle it).

### clickhouse-agent-skills (6 of 6)

All installed — small set, all useful. Maps to [CLAUDE.md § Architecture Rules](../CLAUDE.md#architecture-rules-non-negotiable) 1–8.

| Skill | Purpose |
|---|---|
| `clickhouse-best-practices` | 28 battle-tested rules — primary-key design, partitioning, data types |
| `clickhouse-architecture-advisor` | 5-framework schema design — informs rollup vs raw, MergeTree variant selection |
| `chdb-datastore` / `chdb-sql` | chDB embedded ClickHouse — useful for unit tests that don't want a real CH server |
| `clickhousectl-cloud-deploy` / `clickhousectl-local-dev` | Deployment helpers — we use local-dev for Phase 1 docker-compose, cloud-deploy may inform Hetzner hosting patterns |

### trailofbits-skills (8 of 38)

The full repo has 38 plugins (many are audit-workflow or niche-language). We install only the security-audit primitives that align with Phase 2:

| Skill | Purpose |
|---|---|
| `static-analysis` | CodeQL + Semgrep + SARIF pipeline — backbone of the security gate |
| `semgrep-rule-creator` | Author custom rules (e.g., forbid `WHERE site_id` absence, forbid `Nullable(`) |
| `differential-review` | Compare pre/post-change security posture — PR review aid |
| `insecure-defaults` | Detects unsafe default config — important for our air-gap-capable binary |
| `variant-analysis` | Find variants of a known bug across the codebase |
| `supply-chain-risk-auditor` | Dependency risk scoring — complements our `go-licenses` MIT/Apache/BSD/ISC gate |
| `audit-context-building` | Structured audit reports — we can seed Phase 2 security review evidence |
| `second-opinion` | Independent review pass on critical security-sensitive code |

**Skipped:** smart-contract, Python, Rust, Chrome-extension, Firebase, iOS/macOS-specific, and meta skills (skill-improver, workflow-skill-design).

### marina-skill (4 of 4)

All installed — already a focused set for the Hetzner deploy path.

| Skill | Purpose |
|---|---|
| `server-management` | Create/list/destroy Hetzner servers |
| `server-bootstrap` | Docker + Caddy + deploy user + unattended upgrades |
| `dns-management` | Cloudflare DNS records |
| `app-deployment` | git-push-to-deploy with Docker Compose |

**Iranian DC caveat:** marina-skill targets Hetzner specifically. For Iranian DC deploys (Filimo production), the skill's Cloudflare DNS piece is unused (Iran routes around Cloudflare), and server-bootstrap's `apt` commands need to run against the Iranian DC's mirror. Expect to fork or custom-script for Iran.

## MCP servers

The MCP servers are configured in [`.mcp.json`](../.mcp.json). They run on the **dev host only** and are never bundled into the analytics binary, so their own license dependencies do not fall under the MIT/Apache/BSD/ISC gate.

### clickhouse (Altinity MCP)

- **Image:** `ghcr.io/altinity/altinity-mcp:latest`
- **Why Altinity over the official `mcp-clickhouse`:** doc 23 recommends Altinity for production-grade deploys — OAuth 2.0, JWE auth, TLS, dynamic tools generated from views, hot reload. The official server is fine for local dev; unifying on Altinity keeps one config path.
- **Required env:** `CLICKHOUSE_HOST`, `CLICKHOUSE_PORT`, `CLICKHOUSE_USER`, `CLICKHOUSE_PASSWORD`
- **Setup:** `docker pull ghcr.io/altinity/altinity-mcp:latest` + `docker compose up clickhouse` (from repo root once docker-compose exists).

### gopls

- **Command:** `gopls mcp` — ships natively with recent gopls
- **Install:** `go install golang.org/x/tools/gopls@latest`
- **Capabilities:** govulncheck, test running, coverage, symbol lookup, refactoring

### hetzner (dkruyt/mcp-hetzner)

- **Command:** `mcp-hetzner`
- **Install:** source build from [dkruyt/mcp-hetzner](https://github.com/dkruyt/mcp-hetzner)
- **Required env:** `HCLOUD_TOKEN`
- **Capabilities:** 60+ tools — server provisioning, volumes, firewalls, DNS zones, snapshots, backups

### grafana (grafana/mcp-grafana)

- **Command:** `mcp-grafana`
- **Install:** source build from [grafana/mcp-grafana](https://github.com/grafana/mcp-grafana)
- **Required env:** `GRAFANA_URL`, `GRAFANA_API_KEY`
- **Capabilities:** dashboard queries, Prometheus/Loki/Pyroscope datasources, alerts, incident response

## Phase → tooling map

Lifted from doc 23 §Skills-to-Phase Mapping. See doc 23 for the full rationale.

| Phase | Primary skills | MCP servers |
|---|---|---|
| 0: Setup | `golang-project-layout`, `golang-code-style` | — |
| 1: Ingestion | `golang-concurrency`, `golang-context`, `golang-database`, `clickhouse-best-practices` | `clickhouse` |
| 2: Security | `static-analysis`, `insecure-defaults`, `variant-analysis`, `supply-chain-risk-auditor`, `audit-context-building`, `golang-security`, `golang-safety` | `gopls` (govulncheck) |
| 3: Dashboard API | `golang-database`, `golang-performance`, `clickhouse-architecture-advisor` | `clickhouse` |
| 4: Tracker JS | — (no skill; build from scratch per doc 23 gap) | — |
| 5: Frontend | — (no skill; use [`docs/tech-docs/`](tech-docs/) for Preact/uPlot/Frappe/Jalali refs) | — |
| 6: Config | `golang-cli`, `clickhouse-best-practices` | `clickhouse` |
| 7: Testing | `golang-performance`, `golang-linter`, `differential-review`, `second-opinion` | `gopls`, `clickhouse` |
| 8: Deploy | `server-management`, `server-bootstrap`, `dns-management`, `app-deployment` | `hetzner`, `grafana` |

## Skills Decision Tree (full form)

CLAUDE.md carries the compact form. This is the authoritative version.

```
Task arrives
  Planning / product
  ├─ PRD?                                  → /jaan-to:pm-prd-write
  ├─ User story + BDD AC?                  → /jaan-to:pm-story-write
  ├─ Add to roadmap?                       → /jaan-to:pm-roadmap-add
  ├─ Sprint plan?                          → /jaan-to:pm-sprint-plan
  ├─ Research topic?                       → /jaan-to:pm-research-about

  Backend (Go + ClickHouse)
  ├─ Data model / CH schema?               → /jaan-to:backend-data-model
                                             then `clickhouse-architecture-advisor` +
                                             `clickhouse-best-practices` + clickhouse MCP
  ├─ API contract?                         → /jaan-to:backend-api-contract
  ├─ Scaffold service from spec?           → /jaan-to:backend-scaffold
                                             then `golang-project-layout` + `golang-code-style`
  ├─ Implement service logic?              → /jaan-to:backend-service-implement
                                             then cc-skills-golang (concurrency, context, database)
  ├─ Task breakdown?                       → /jaan-to:backend-task-breakdown
  ├─ Go concurrency / context?             → `golang-concurrency` / `golang-context`
  ├─ DB query tuning?                      → `golang-database` +
                                             `clickhouse-best-practices` + clickhouse MCP
  ├─ Performance / profiling?              → `golang-performance` + `gopls` MCP
  ├─ CLI / config?                         → `golang-cli`
  ├─ Observability / slog?                 → `golang-observability`

  Security (Phase 2)
  ├─ Static analysis?                      → `static-analysis` + `golang-security` +
                                             `gopls` MCP (govulncheck)
  ├─ Authoring Semgrep rules?              → `semgrep-rule-creator`
  ├─ Supply chain / deps audit?            → `supply-chain-risk-auditor`
  ├─ Insecure defaults hunt?               → `insecure-defaults`
  ├─ Variant of known bug?                 → `variant-analysis`
  ├─ Building audit evidence?              → `audit-context-building`
  ├─ Second opinion on risky change?       → `second-opinion`
  ├─ Remediate findings?                   → /jaan-to:sec-audit-remediate
  ├─ Engineering audit / scoring?          → /jaan-to:detect-dev

  Review
  ├─ Backend PR review?                    → /jaan-to:backend-pr-review
  ├─ Pre/post change diff review?          → `differential-review`

  Testing (Phase 7)
  ├─ BDD / Gherkin cases?                  → /jaan-to:qa-test-cases
  ├─ Runnable tests from cases?            → /jaan-to:qa-test-generate
  ├─ Run / diagnose / auto-fix?            → /jaan-to:qa-test-run
  ├─ Linter / code quality?                → `golang-linter` + `golang-code-style`
  ├─ Memory / race safety?                 → `golang-safety`

  Frontend (Phase 5)
  ├─ Scaffold Preact component?            → /jaan-to:frontend-scaffold
  ├─ Distinctive UI design?                → /jaan-to:frontend-design
  ├─ Task breakdown?                       → /jaan-to:frontend-task-breakdown
  ├─ User flow diagrams?                   → /jaan-to:ux-flowchart-generate
  ├─ Microcopy / i18n (Persian/English)?   → /jaan-to:ux-microcopy-write

  Docs / references
  ├─ Fetch library docs?                   → /jaan-to:dev-docs-fetch (Context7 MCP)
                                             fallback: docs/tech-docs/ (16 cached refs)

  Deploy (Phase 8)
  ├─ CI/CD / Docker scaffolds?             → /jaan-to:devops-infra-scaffold
  ├─ Provision server?                     → `server-management` + `hetzner` MCP
  ├─ Bootstrap server (Docker/Caddy)?      → `server-bootstrap`
  ├─ Deploy app?                           → `app-deployment` +
                                             /jaan-to:devops-deploy-activate
  ├─ DNS records?                          → `dns-management` (Cloudflare)
  ├─ Verify running build?                 → /jaan-to:dev-verify
  ├─ Monitoring dashboards / alerts?       → `grafana` MCP

  Gaps (no skill — build from scratch)
  ├─ Tracker (<2 KB IIFE)?                 → hand-build per Phase 4 plan
  ├─ uPlot / Frappe Charts?                → docs/tech-docs/ + hand-build
  ├─ Jalali calendar?                      → integrate `jalaali-js` directly
  ├─ WAL durability?                       → `tidwall/wal` library directly
  ├─ BLAKE3 identity hashing?              → `lukechampine.com/blake3` directly

  └─ Unknown?                              → re-read this file; if still unclear, ask
```

## Known gaps — custom skill TODOs

Per doc 23 §Gap Analysis, these have **no community skill coverage**. Author custom skills only when the corresponding phase opens ([CLAUDE.md § Architecture Rule 7](../CLAUDE.md#architecture-rules-non-negotiable) — defer before building):

| Gap | Phase | Recommended approach |
|---|---|---|
| Vanilla JS <2 KB tracker | 4 | Build from scratch; use `docs/tech-docs/` for `sendBeacon` + IIFE patterns |
| uPlot / Frappe Charts | 5 | Generate on demand from [`docs/tech-docs/uplot.md`](tech-docs/) |
| Jalali / Persian calendar | 5 (v1.1) | Integrate `jalaali-js` (3 KB, MIT) directly |
| WAL durability | 1 | Use `tidwall/wal` library directly; cc-skills-golang concurrency covers the surrounding Go patterns |
| BLAKE3-128 identity | 1 | Use `lukechampine.com/blake3` (MIT) directly |
| Iranian DC deploy | 8 | No community skill. Fork marina-skill or write plain shell scripts against Iranian DC API |

## Maintenance

- **30-skill cap:** Doc 23 warns that Claude Code's skill-discovery performance degrades past 30 visible skills. Current install is at 32 (the `static-analysis` plugin nests 3 sub-skills). Adding a new skill means removing one — favor removing cc-skills-golang entries that have gone unused in practice.
- **Skill updates:** None of the skills are tracked as git submodules. To update, re-clone the source repo and `cp -R` the updated `skills/<name>/` directory, preserving our `SOURCE` and `LICENSE.source` files.
- **Air-gap invariant:** Skills must not embed remote fetches that execute at load time. Before adding a new skill, grep the SKILL.md for `curl`, `wget`, bare `https://` → the skill may instruct Claude to fetch at runtime, which breaks [CLAUDE.md § Isolation / Air-Gapped Capability](../CLAUDE.md#isolation--air-gapped-capability-non-negotiable).
- **License attestation:** Each skill directory carries a `SOURCE` file (1-liner) and `LICENSE.source` file (full license text from the source repo). These are part of the repo and survive updates.
