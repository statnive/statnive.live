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

- **Skill count ceiling (revised):** Doc 23's 30-skill cap was a heuristic. Doc 25 re-evaluates: the trigger-pattern clarity rule matters more than raw count — "install only skills whose trigger patterns you can articulate in one sentence". Post-doc-25, this project runs **~53 skills** (30 doc-23 foundation + 17 community additions + 6 custom). Watch for false activations; remove any community skill that fires on tasks it wasn't designed for.
- **Skill updates:** None of the skills are tracked as git submodules. To update, re-clone the source repo and `cp -R` the updated `skills/<name>/` directory, preserving our `SOURCE` and `LICENSE.source` files.
- **Air-gap invariant:** Skills must not embed remote fetches that execute at load time. Before adding a new skill, grep the SKILL.md for `curl`, `wget`, bare `https://` → the skill may instruct Claude to fetch at runtime, which breaks [CLAUDE.md § Isolation / Air-Gapped Capability](../CLAUDE.md#isolation--air-gapped-capability-non-negotiable).
- **License attestation:** Each skill directory carries a `SOURCE` file (1-liner) and `LICENSE.source` file (full license text from the source repo). These are part of the repo and survive updates.

---

## Doc 25 additions (Weeks 1–12)

**Summary.** [`jaan-to/docs/research/25-ai-claude-skills-filimo-grade-analytics-platform.md`](../../jaan-to/docs/research/25-ai-claude-skills-filimo-grade-analytics-platform.md) refines doc 23 with a vetted install matrix, an explicit blacklist, and six **mandatory** project-local custom skills that encode the 8 architecture rules from [`CLAUDE.md`](../CLAUDE.md) as CI-blocking guardrails. 12-week install order front-loads security and tenancy foundations.

### Community skills added by doc 25

| Skill | Source repo | License | Install path | Installed |
|---|---|---|---|---|
| `skill-creator` | anthropics/skills | MIT | `.claude/skills/skill-creator/` | ✓ |
| `webapp-testing` | anthropics/skills | MIT | `.claude/skills/webapp-testing/` | ✓ |
| `frontend-design` | anthropics/skills | MIT | `.claude/skills/frontend-design/` | ✓ (with CDN-font override, see below) |
| `use-modern-go` | JetBrains/go-modern-guidelines | Apache-2.0 | `.claude/skills/use-modern-go/` | ✓ |
| `owasp-security` | agamm/claude-code-owasp | MIT | `.claude/skills/owasp-security/` | ✓ |
| `VibeSec-Skill` | BehiSecc/VibeSec-Skill | MIT | `.claude/skills/vibesec/` | ✓ |
| `ctm` | izar/tm_skills | CC-BY-4.0 | `.claude/skills/ctm/` | ✓ |
| `4qpytm` | izar/tm_skills | CC-BY-4.0 | `.claude/skills/4qpytm/` | ✓ |
| `web-design-guidelines` | vercel-labs/agent-skills | MIT | `.claude/skills/web-design-guidelines/` | ✓ (with CDN-font override) |
| `react-best-practices` | vercel-labs/agent-skills | MIT | `.claude/skills/react-best-practices/` | ✓ (bundle-size rules cherry-picked; Preact ≠ React re-render model) |
| `brainstorming` | obra/superpowers | MIT | `.claude/skills/brainstorming/` | ✓ |
| `writing-plans` | obra/superpowers | MIT | `.claude/skills/writing-plans/` | ✓ |
| `subagent-driven-development` | obra/superpowers | MIT | `.claude/skills/subagent-driven-development/` | ✓ |
| `verification-before-completion` | obra/superpowers | MIT | `.claude/skills/verification-before-completion/` | ✓ |
| `systematic-debugging` | obra/superpowers | MIT | `.claude/skills/systematic-debugging/` | ✓ |
| `constant-time-analysis` | trailofbits/skills | CC-BY-SA-4.0 | `.claude/skills/constant-time-analysis/` | ✓ |
| `knip-unused-code-dependency-finder` | agentskillexchange/skills | CC-BY-SA-4.0 | `.claude/skills/knip-unused-code-dependency-finder/` | ✓ |

**Frontend-design / web-design-guidelines clamp:** both default to CDN fonts. Claude must override to emit Preact-compatible output with self-hosted fonts only — this is enforced by the `air-gap-validator` + `preact-signals-bundle-budget` custom skills.

**Obra/superpowers:** only 5 of 14 skills installed. The remaining 9 (`using-git-worktrees`, `finishing-a-development-branch`, `requesting-code-review`, `receiving-code-review`, `test-driven-development`, `dispatching-parallel-agents`, `writing-skills`, `using-superpowers`, `executing-plans`) are skipped to avoid bloat. Re-evaluate post-launch.

### Custom skills catalog (doc 25 §gap-analysis)

Six `.claude/skills/*` directories scaffolded; bodies fill in per phase. Each has `SKILL.md` (frontmatter + trigger) and `README.md` (full spec).

| Skill | Architecture rule | Trigger | Required before |
|---|---|---|---|
| [`tenancy-choke-point-enforcer`](../.claude/skills/tenancy-choke-point-enforcer/README.md) | Rule 8 | SQL gen/mod in `internal/storage/` | First storage code (merged PR #9) |
| [`air-gap-validator`](../.claude/skills/air-gap-validator/README.md) | Isolation | `go get`, new deps, net code | First `go.mod` addition |
| [`clickhouse-rollup-correctness`](../.claude/skills/clickhouse-rollup-correctness/README.md) | Rule 2 | `AggregatingMergeTree` DDL, MV creation | First MV DDL (shipped in Phase 1) |
| [`clickhouse-cluster-migration`](../.claude/skills/clickhouse-cluster-migration/README.md) | `{{if .Cluster}}` (doc 24 §Migration 0029) | New migration file | First migration (shipped) |
| [`preact-signals-bundle-budget`](../.claude/skills/preact-signals-bundle-budget/README.md) | Stack (50KB/15KB-gz + 1.2KB/600B-gz) | Frontend changes | First Preact component (Phase 5) / first tracker build (Phase 4) |
| [`blake3-hmac-identity-review`](../.claude/skills/blake3-hmac-identity-review/README.md) | Privacy Rules 2, 3, 4 | Crypto / identity code | First identity code (shipped) |

### Blacklist (do not install)

| Skill | Why |
|---|---|
| `anthropics/skills/web-artifacts-builder` | React 18 + Tailwind + shadcn + Parcel + html-inline — pulls network deps at build, blows past 50KB/15KB-gz dashboard budget. Air-gap violation. |
| `shajith003/awesome-claude-skills` | AI-generated boilerplate; low signal. |
| `sickn33/antigravity-awesome-skills` | Claims 1,431+ skills, mostly auto-generated duplicates; inflated counts. |
| `rohitg00/awesome-claude-code-toolkit` | Inflated aggregate count, low signal-to-noise. |

### 12-week install order (doc 25 §priority-ranking)

**Week 1 — security & tenancy foundations** (launch-critical):
- `samber/cc-skills-golang` (full bundle — already 12/37 installed; expand to 37 is a follow-up)
- `ClickHouse/agent-skills` (already installed)
- `trailofbits/skills` (already installed + `constant-time-analysis` added by this PR)
- `anthropics/skills` cherry-pick (skill-creator, template, webapp-testing, frontend-design)
- **Custom `tenancy-choke-point-enforcer`** — before any new storage code merges
- **Custom `air-gap-validator`** — before any new dep is added

**Weeks 2–4 — performance & correctness:**
- `JetBrains/use-modern-go`
- `agamm/claude-code-owasp` + `BehiSecc/VibeSec-Skill`
- `obra/superpowers` 5-skill subset
- **Custom `clickhouse-rollup-correctness`** — before any new MV DDL
- **Custom `clickhouse-cluster-migration`** — before any new migration file

**Weeks 5–8 — frontend & crypto hardening:**
- `vercel-labs/agent-skills` (web-design-guidelines primary; react-best-practices for bundle rules)
- `agentskillexchange/knip-unused-code-dependency-finder`
- `izar/tm_skills` (ctm, 4qpytm)
- **Custom `preact-signals-bundle-budget`** — Phase 4/5 load-bearing
- **Custom `blake3-hmac-identity-review`** — Phase 1+ regression guard

**Weeks 9–12 — launch hardening** (not always-on):
- Run `AgriciDaniel/claude-cybersecurity` one-shot pre-launch audit (not installed; invoke from operator Claude session)
- Run `fr33d3m0n/threat-modeling` full STRIDE pass on salt-rotation + air-gap stories
- Defer `tracker-beacon-reliability` custom skill and any other v1.1 skill unless incidents materialize.

### Follow-ups (out of scope for the doc-25 install PR)

- Expand `cc-skills-golang` from 12 → full 37 (doc 25 warns against partial installs).
- Fill in Semgrep rule bodies + test fixtures for the 6 custom skills (40–60 hrs total, scheduled per phase).
- Wire `.claude/settings.json` hooks + `.githooks/pre-commit` to invoke custom skills via `claude-code` headless mode.
- Install one of `qualixar/skillfortify` or `relaxcloud-cn/clawsafety` to vet future community installs (CycloneDX SBOM + `skill-lock.json`).
