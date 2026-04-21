# Dev Tooling — Claude Code skills + MCP servers

Referenced from [`CLAUDE.md`](../CLAUDE.md) § Dev Tooling. This file holds the detail; CLAUDE.md holds the compact routing. When these two drift, this file wins.

All recommendations trace back to [`../../jaan-to/docs/research/23-ai-workflow-claude-skills-go-clickhouse-analytics.md`](../../jaan-to/docs/research/23-ai-workflow-claude-skills-go-clickhouse-analytics.md) (doc 23). We do **not** restate the research here — only decisions and deviations.

**Historical accretion** (what docs 25 / 27 / 28 each added on what schedule): [`docs/history/skill-roster-evolution.md`](history/skill-roster-evolution.md). Current-state status matrix lives in [§ Scaffolded-skill activation convention](#scaffolded-skill-activation-convention) below.

## Install summary

**4 original skill collections** (doc 23 foundation, 32 atomic skills — 2 over doc 23's 30 soft cap; `static-analysis` nests 3 sub-skills):

| Collection | Source | License | Installed | Phase coverage |
|---|---|---|---|---|
| cc-skills-golang | [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang) | MIT | 12 of 37 (curated) | 0, 1, 3, 6, 7 |
| clickhouse-agent-skills | [ClickHouse/agent-skills](https://github.com/ClickHouse/agent-skills) | Apache-2.0 | 1 primary (`clickhouse-best-practices`, 28 rules/11 categories) + 4 auxiliary (`chdb-datastore`, `chdb-sql`, `clickhousectl-cloud-deploy`, `clickhousectl-local-dev`) | 0, 1, 3, 6 |
| trailofbits-skills | [trailofbits/skills](https://github.com/trailofbits/skills) | CC-BY-SA-4.0 | 8 of 38 (curated) | 2 (security) |
| marina-skill | [The-Focus-AI/marina-skill](https://github.com/The-Focus-AI/marina-skill) | MIT | 4 of 4 (all) | 8 (deploy) |

**4 MCP servers** in [`.mcp.json`](../.mcp.json): `clickhouse` (Altinity), `gopls`, `hetzner`, `grafana`.

Plus 17 doc-25/27 community additions + 14 project-local custom skills — full list in [`history/skill-roster-evolution.md`](history/skill-roster-evolution.md).

## Licensing decisions

CLAUDE.md § License Rules mandates MIT/Apache/BSD/ISC for anything **in the binary**. Skills and MCP servers are dev-time tooling — not bundled — so the gate applies to the Go dependency tree, not this directory. Each skill source verified:

| Collection | License | Verdict | Rationale |
|---|---|---|---|
| cc-skills-golang | MIT | ✅ Install | Green on both. |
| clickhouse-agent-skills | Apache-2.0 | ✅ Install | Green on both. |
| marina-skill | MIT (declared; no LICENSE file) | ✅ Install | Dev-time. |
| trailofbits-skills | CC-BY-SA-4.0 | ⚠️ Install unmodified | Share-alike applies to modifications; ship verbatim. Fork under CC-BY-SA if modifying. |
| darrenoakey/claude-skill-golang | **CC-BY-NC-4.0** | ❌ Rejected | Non-commercial only; statnive-live is sold commercially. CI gate functionality covered by `.githooks/pre-commit` + `make lint`. |

## Curated skill list

### cc-skills-golang (12 of 37)

Installed per phase (Architecture Rule 7 — defer before building):

| Skill | Purpose |
|---|---|
| `golang-concurrency` | Goroutines/channels/errgroup/singleflight — Phase 1 pipeline |
| `golang-context` | Context propagation, cancellation, deadlines |
| `golang-database` | Connection pools, `database/sql` — Phase 1, 3 |
| `golang-error-handling` | Error wrapping, sentinel, errors.Is/As |
| `golang-security` | Crypto, hashing, TLS, input validation — Phase 2 |
| `golang-performance` | Profiling, benchmarking, allocations — Phase 1, 7 |
| `golang-code-style` | Naming, formatting, idioms |
| `golang-cli` | Flag parsing, subcommands — Phase 6 config |
| `golang-observability` | `slog`, metrics, traces — Phase 2/8 |
| `golang-project-layout` | Standard layout — Phase 0 |
| `golang-linter` | golangci-lint, staticcheck — Phase 7 gate |
| `golang-safety` | `unsafe`, races, memory safety — Phase 1, 2 |

### clickhouse-agent-skills (1 primary + 4 auxiliary)

Upstream ships **one** skill (`clickhouse-best-practices`, 28 rules/11 categories). The 4 auxiliary artifacts are related but separate — useful for embedded-CH unit tests (Phase 7) + local-dev setup.

| Skill | Purpose |
|---|---|
| `clickhouse-best-practices` | 28 battle-tested rules — primary-key, partitioning, data types, JOINs, batching, mutations, MV, async inserts, OPTIMIZE avoidance, JSON |
| `clickhouse-architecture-advisor` | 5-framework schema design — rollup vs raw, MergeTree variant selection |
| `chdb-datastore` / `chdb-sql` | chDB embedded CH — unit tests without a real server |
| `clickhousectl-cloud-deploy` / `clickhousectl-local-dev` | Deployment helpers; local-dev for Phase 1 docker-compose |

### trailofbits-skills (8 of 38)

Security-audit primitives for Phase 2:

| Skill | Purpose |
|---|---|
| `static-analysis` | CodeQL + Semgrep + SARIF — security gate backbone |
| `semgrep-rule-creator` | Author custom rules (e.g., forbid `Nullable(`) |
| `differential-review` | Pre/post-change security posture — PR review aid |
| `insecure-defaults` | Unsafe defaults hunt — important for air-gap binary |
| `variant-analysis` | Find variants of a known bug |
| `supply-chain-risk-auditor` | Dep risk scoring — complements `go-licenses` |
| `audit-context-building` | Structured audit reports — seeds Phase 2 evidence |
| `second-opinion` | Independent review pass on risky change |

### marina-skill (4 of 4)

| Skill | Purpose |
|---|---|
| `server-management` | Create/list/destroy Hetzner servers |
| `server-bootstrap` | Docker + Caddy + deploy user + unattended upgrades |
| `dns-management` | Cloudflare DNS records |
| `app-deployment` | git-push-to-deploy with Docker Compose |

**Iranian DC caveat:** Hetzner-specific. Cloudflare DNS unused (Iran routes around CF); server-bootstrap's `apt` needs Iranian mirror. Expect to fork or custom-script for Iran.

## MCP servers

Configured in [`.mcp.json`](../.mcp.json). Dev host only — never bundled into the binary.

### clickhouse (Altinity MCP)

- **Image:** `ghcr.io/altinity/altinity-mcp:latest`
- **Why Altinity over official `mcp-clickhouse`:** production-grade — OAuth 2.0, JWE auth, TLS, dynamic tools from views, hot reload.
- **Env:** `CLICKHOUSE_HOST`, `CLICKHOUSE_PORT`, `CLICKHOUSE_USER`, `CLICKHOUSE_PASSWORD`
- **Setup:** `docker pull ghcr.io/altinity/altinity-mcp:latest` + `docker compose up clickhouse`.

### gopls

- **Command:** `gopls mcp` (ships with recent gopls)
- **Install:** `go install golang.org/x/tools/gopls@latest`
- **Capabilities:** govulncheck, tests, coverage, symbol lookup, refactoring

### hetzner (dkruyt/mcp-hetzner)

- **Install:** source build from [dkruyt/mcp-hetzner](https://github.com/dkruyt/mcp-hetzner)
- **Env:** `HCLOUD_TOKEN`
- **Capabilities:** 60+ tools — provisioning, volumes, firewalls, DNS zones, snapshots, backups

### grafana (grafana/mcp-grafana)

- **Install:** source build from [grafana/mcp-grafana](https://github.com/grafana/mcp-grafana)
- **Env:** `GRAFANA_URL`, `GRAFANA_API_KEY`
- **Capabilities:** dashboard queries, Prometheus/Loki/Pyroscope, alerts, incident response

## Phase → tooling map

Lifted from doc 23 §Skills-to-Phase Mapping:

| Phase | Primary skills | MCP |
|---|---|---|
| 0: Setup | `golang-project-layout`, `golang-code-style` | — |
| 1: Ingestion | `golang-concurrency`, `golang-context`, `golang-database`, `clickhouse-best-practices` | `clickhouse` |
| 2: Security | `static-analysis`, `insecure-defaults`, `variant-analysis`, `supply-chain-risk-auditor`, `audit-context-building`, `golang-security`, `golang-safety` | `gopls` |
| 3: Dashboard API | `golang-database`, `golang-performance`, `clickhouse-architecture-advisor` | `clickhouse` |
| 4: Tracker JS | — (no skill; build from scratch per doc 23 gap) | — |
| 5: Frontend | — (use [`tech-docs/`](tech-docs/) for Preact/uPlot/Frappe/Jalali refs) | — |
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

Per doc 23 §Gap Analysis — no community skill coverage. Author when the corresponding phase opens:

| Gap | Phase | Approach |
|---|---|---|
| Vanilla JS <2 KB tracker | 4 | Build from scratch; see `docs/tech-docs/` for `sendBeacon` + IIFE |
| uPlot / Frappe Charts | 5 | On-demand from [`docs/tech-docs/uplot.md`](tech-docs/) |
| Jalali / Persian calendar | 5 (v1.1) | `jalaali-js` (3 KB, MIT) directly |
| WAL durability | 1 | `tidwall/wal` directly; cc-skills-golang covers surrounding patterns |
| BLAKE3-128 identity | 1 | `lukechampine.com/blake3` (MIT) directly |
| Iranian DC deploy | 8 | No community skill — fork marina-skill or plain shell against Iranian DC API |

## Maintenance

- **Skill count ceiling (revised):** Doc 23's 30-skill cap was a heuristic. Doc 25 re-evaluates: trigger-pattern clarity matters more than raw count — "install only skills whose trigger patterns you can articulate in one sentence". Post-doc-25, the project runs ~63 skills (30 doc-23 foundation + 17 community additions + 14 custom + 2 doc-27 adjacent). Watch for false activations; remove any skill that fires outside its design envelope.
- **Skill updates:** Not tracked as git submodules. To update, re-clone source repo and `cp -R` the updated `skills/<name>/` directory, preserving `SOURCE` and `LICENSE.source` files.
- **Air-gap invariant:** Skills must not embed remote fetches that execute at load time. Before adding a new skill, grep `SKILL.md` for `curl`, `wget`, bare `https://` — the skill may instruct Claude to fetch at runtime, breaking [CLAUDE.md § Isolation](../CLAUDE.md#isolation--air-gapped-capability-non-negotiable).
- **License attestation:** Each skill directory carries `SOURCE` (1-liner) and `LICENSE.source` (full license text from source repo). Part of the repo; survives updates.

### Scaffolded-skill activation convention

Custom skills in this project are authored **ahead of** their enforcement phase. A skill's `SKILL.md` body describes the contract it will enforce; its Semgrep rule bodies and CI workflow land when the matching phase opens (e.g. `iranian-dc-deploy` scaffolds in Phase 0, gates from Phase 8). Without a marker, every custom skill would false-activate on every glob match across the intervening phases.

**Convention.** Every custom `SKILL.md` under `.claude/skills/` whose enforcement is scheduled for a future phase prepends a blockquote activation-gate preamble immediately after the `# <skill-name>` heading:

```markdown
# <skill-name>

> **Activation gate (Phase N, <scope>).** This skill's Semgrep rule bodies
> and CI wiring are scheduled for Phase N (<what ships there>). Until the
> corresponding `.github/workflows/<skill>-gate.yml` is green on main,
> treat this skill as **advisory-only** — surface the checklist to the
> reviewer, do not block merges, and flag any mismatch as
> `activation-pending` rather than auto-fixing.

<regular body continues here...>
```

**Scope phrasing:**
- `Phase N` alone (e.g. `Phase 1`) for single-phase skills.
- `Phase N, Weeks X–Y` for doc-28-scheduled skills with calendar anchoring.
- `Phase N — partially live` for skills where some rules are live (e.g. `wal-durability-review` post-PR #25).
- `advisory-only by design` for skills shipping no Semgrep rules (e.g. `clickhouse-upgrade-playbook`).

**When to remove the preamble.** Operator drops the blockquote when the matching CI workflow is green on main. Not before — and never as part of an unrelated PR; removal is its own commit so PR review sees the activation event.

**Why this, not `disabled: true` frontmatter.** Claude Code's schema handling for unknown frontmatter keys is not documented to silently ignore. The blockquote is (a) human-readable in both Claude Code's skill-picker and GitHub rendering, (b) machine-scannable via `grep -l 'Activation gate' .claude/skills/*/SKILL.md`, (c) survives any future frontmatter schema change.

**Current status (as of 2026-04-21) — all 14 custom skills carry the preamble:**

| Skill | Phase | Gate state |
|---|---|---|
| `tenancy-choke-point-enforcer` | 1 | advisory |
| `air-gap-validator` | 0 (ongoing) | advisory |
| `clickhouse-rollup-correctness` | 1 | advisory |
| `clickhouse-cluster-migration` | 1 lint + P5 upgrade | advisory |
| `preact-signals-bundle-budget` | 4 (tracker) + 5 (dashboard) | advisory |
| `blake3-hmac-identity-review` | 1 | advisory |
| `wal-durability-review` | 7b | **partially live** (Semgrep + kill-9 in PR #25) |
| `ratelimit-tuning-review` | 10 | advisory (HARD GATE pending Filimo cutover) |
| `gdpr-code-review` | 11 | advisory (HARD GATE pending SaaS signup) |
| `dsar-completeness-checker` | 11 | advisory (HARD GATE pending SaaS signup) |
| `iranian-dc-deploy` | 8 W17–18 | advisory (HARD GATE pending Filimo cutover) |
| `geoip-pipeline-review` | 8 W19–20 | advisory |
| `clickhouse-operations-review` | 8 W20–22 | advisory |
| `clickhouse-upgrade-playbook` | 8+ / P5 | **advisory-only by design** (no Semgrep rules) |

## Historical accretion

Doc-25/27/28 roster evolution — what each doc added, on what schedule, with follow-ups — lives in [`history/skill-roster-evolution.md`](history/skill-roster-evolution.md). Includes: doc 25 community additions + 6 custom skills + 12-week install order; doc 27 three-gap closure (WAL / CGNAT / GDPR-on-HLL) + 4 custom skills + ASN-DB licensing decision; doc 28 final-three-gap closure (GeoIP / Iranian-DC / CH-ops) + 4 custom skills + CC-BY-SA policy correction + ClickHouse roster correction.

## GeoIP licensing — three-tier posture

Doc 28 §Gap 1 surfaced the CC-BY-SA-4.0 carve-out as a single policy decision. It's really three, one per deployment tier. Cross-referenced from [`CLAUDE.md § License Rules`](../CLAUDE.md#license-rules-critical).

| Tier | DB version | License | Attribution surfaces | LITE account owner | Status |
|---|---|---|---|---|---|
| **Dev / staging** (Hetzner, all operators) | IP2Location LITE DB23 | CC-BY-SA-4.0 (carve-out) | LICENSE-third-party.md + `/about` JSON + dashboard footer (all three mandatory, verbatim string) | **Each operator registers own** at [lite.ip2location.com](https://lite.ip2location.com) — third-party redistribution forbidden by LITE ToS | ✅ Decided (doc 28 §Gap 1) |
| **Filimo production** (Phase 10 cutover) | IP2Location **paid DB23 Site License** | Commercial | **Waived** per commercial terms | statnive-live org (sales contract) | ✅ Decided — budget `$99–$980/yr`; Semgrep rule skipped via build tag `licensed_db23` |
| **SaaS tier** (Phase 11+ public signup) | **Decision required** | **Decision required** | **Decision required** | **Decision required** | ⚠️ **Open** — resolve pre-Phase-11 design |

### The SaaS decision — why it's genuinely ambiguous

LITE's ToS explicitly forbids third-party redistribution. For self-hosted customers (current dev/staging tier), each operator registers their own LITE account + SCPs their own BIN. For SaaS, we're hosting dashboards on behalf of EU customers and looking up their visitors' IPs against *our* LITE BIN. Two defensible readings:

- **Reading A: we are the data processor, LITE is input data.** No redistribution — we don't ship LITE to the customer; we use it internally. Analogous to any SaaS using a proprietary dataset. Processor-DPA language covers the usage.
- **Reading B: serving geo-data in dashboards *is* redistribution.** The API response (`country`, `city`, `region`) is derived from LITE under our license. If a customer's visitor data includes a geo-lookup result, we've effectively redistributed LITE's mapping.

Reading B is the more conservative interpretation. If adopted, SaaS tier must go paid-DB23 from launch, budget accordingly.

**Required before Phase 11 design work starts:**
1. Legal call on Reading A vs Reading B interpretation of LITE ToS.
2. If Reading A holds: processor-DPA draft language explicitly claiming LITE as input data, not redistribution.
3. If Reading B holds: paid DB23 Site License quote for SaaS-tier usage (likely different SKU than Filimo's single-site license).
4. Decide attribution-surface policy for SaaS — even with paid license, the dashboard footer is visible to EU end-users; some operators may want attribution voluntarily retained for transparency.

**Do not** ship the SaaS tier with LITE-and-no-DPA-language. That's the ambiguous middle ground that generates worst-case legal exposure.
