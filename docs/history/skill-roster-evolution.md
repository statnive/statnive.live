# Skill roster evolution — historical accretion (docs 25 / 27 / 28)

> Referenced from [`tooling.md`](../tooling.md). What each research doc added to the skill roster, on what schedule, with what follow-ups. Current-state inventory lives in [`tooling.md § Scaffolded-skill activation convention`](../tooling.md#scaffolded-skill-activation-convention) (14-row status matrix) + [`CLAUDE.md § Custom-skill triggers`](../../CLAUDE.md#custom-skill-triggers-project-local-guardrails--fire-automatically). This file is for "how did we get here?" questions, not "what's installed now?".

## Doc 25 additions (Weeks 1–12)

**Summary.** [`jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md`](../../../jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md) refines doc 23 with a vetted install matrix, an explicit blacklist, and six **mandatory** project-local custom skills that encode the 8 architecture rules from [`CLAUDE.md`](../../CLAUDE.md) as CI-blocking guardrails. 12-week install order front-loads security and tenancy foundations.

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

**Frontend-design / web-design-guidelines clamp:** both default to CDN fonts. Claude must override to emit Preact-compatible output with self-hosted fonts only — enforced by the `air-gap-validator` + `preact-signals-bundle-budget` custom skills.

**Obra/superpowers:** only 5 of 14 skills installed. The remaining 9 (`using-git-worktrees`, `finishing-a-development-branch`, `requesting-code-review`, `receiving-code-review`, `test-driven-development`, `dispatching-parallel-agents`, `writing-skills`, `using-superpowers`, `executing-plans`) are skipped to avoid bloat. Re-evaluate post-launch.

### Custom skills catalog (doc 25 §gap-analysis)

Six `.claude/skills/*` directories scaffolded; bodies fill in per phase.

| Skill | Architecture rule | Trigger | Required before |
|---|---|---|---|
| [`tenancy-choke-point-enforcer`](../../.claude/skills/tenancy-choke-point-enforcer/README.md) | Rule 8 | SQL gen/mod in `internal/storage/` | First storage code (merged PR #9) |
| [`air-gap-validator`](../../.claude/skills/air-gap-validator/README.md) | Isolation | `go get`, new deps, net code | First `go.mod` addition |
| [`clickhouse-rollup-correctness`](../../.claude/skills/clickhouse-rollup-correctness/README.md) | Rule 2 | `AggregatingMergeTree` DDL, MV creation | First MV DDL (shipped in Phase 1) |
| [`clickhouse-cluster-migration`](../../.claude/skills/clickhouse-cluster-migration/README.md) | `{{if .Cluster}}` (doc 24 §Migration 0029) | New migration file | First migration (shipped) |
| [`preact-signals-bundle-budget`](../../.claude/skills/preact-signals-bundle-budget/README.md) | Stack (50KB/15KB-gz + 1.2KB/600B-gz) | Frontend changes | First Preact component (Phase 5) / first tracker build (Phase 4) |
| [`blake3-hmac-identity-review`](../../.claude/skills/blake3-hmac-identity-review/README.md) | Privacy Rules 2, 3, 4 | Crypto / identity code | First identity code (shipped) |

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
- `trailofbits/skills` (already installed + `constant-time-analysis` added)
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

---

## Doc 27 additions (close the three gaps)

**Summary.** [`jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md`](../../../jaan-to/docs/research/27-closing-three-skill-gaps-statnive-live.md) surveys ~2,000 skills across 10 aggregators and confirms that no community skill targets the three surfaces statnive-live most exposes: **WAL durability** (`tidwall/wal` binary format doesn't CRC individual records; fsyncgate 2018 data-loss class on Linux pre-4.13), **CGNAT-aware rate limiting** (Iranian AS44244 / AS197207 / AS57218 NAT444 makes 100 req/s per-IP catastrophic), and **GDPR on append-only HyperLogLog** (row-level erasure impossible by design; defensible legal position uses Recital 26 + C-413/23 + weekly rollup rebuild).

### Three opinionated defaults (adopt)

- **(a) WAL: ack-after-fsync with group commit.** One goroutine fsyncs every 100 ms; all waiting handlers return 200 together. ~50 ms p50 latency cost is within the 500 ms p99 SLO. Only config that honors the kill-9 guarantee as worded. Phase 7b gate.
- **(b) Rate limit: Iranian-ASN-aware tiering.** Compound key `(ip, site_id)` at 1 K req/s sustained / 2 K burst for AS44244 / AS197207 / AS57218; 100/200 fallback elsewhere; per-`site_id` global ceiling 25 K req/s. ASN DB: **`iptoasn.com`** public-domain TSV. Phase 10 cutover gate.
- **(c) GDPR: declare HLL rollups anonymous under Recital 26.** DPA language + weekly rollup rebuild as bounded-time (max 1 week) compliance safety net. Phase 11 signup gate.

### ASN-DB licensing decision (rules out MaxMind + IPLocate)

| Source | License | Verdict |
|---|---|---|
| MaxMind GeoLite2 (MMDB) | CC-BY-SA-4.0 | ❌ Rejected — share-alike contaminates binary, violates [CLAUDE.md § License Rules](../../CLAUDE.md#license-rules-critical) |
| IPLocate.io free DB | CC-BY-SA | ❌ Rejected — same issue |
| **iptoasn.com `ip2asn-v4.tsv.gz`** | **Public domain** | ✅ **Adopted** — hourly-refreshed TSV, TSV loader, operator file-drop matches air-gap rule |

### Adjacent community skills added by doc 27

| Skill | Source repo | License | Install path | Role |
|---|---|---|---|---|
| `grc-gdpr` | [Sushegaad/Claude-Skills-Governance-Risk-and-Compliance](https://github.com/Sushegaad/Claude-Skills-Governance-Risk-and-Compliance) (GDPR module only) | MIT | `.claude/skills/grc-gdpr/` | Outer GDPR checklist, findings mapped to Articles |
| `legal-compliance-check` | [anthropics/knowledge-work-plugins](https://github.com/anthropics/knowledge-work-plugins) → `legal/skills/compliance-check` | Apache-2.0 | `.claude/skills/legal-compliance-check/` | Regulatory-review template; seeds Article 28 DPA for SaaS |

**Not installed** (doc 27's `BehiSecc/sanitize` reference — repo does not exist): doc 27 line 7 cites a `BehiSecc/sanitize` skill detecting "15 categories of PII"; `github.com/BehiSecc/sanitize` returns 404 and the org publishes `VibeSec-Skill` + `bugSkills` + `awesome-claude-skills` only. The functional role — last-mile PII grep over audit-log output — is covered by the custom `gdpr-code-review` skill's `semgrep/pii-rules.yml` (TODO, Phase 11) and by the existing [`vibesec`](../../.claude/skills/vibesec/SKILL.md) install.

### Custom skills catalog (doc 27 §gap-analysis)

Four `.claude/skills/*` directories scaffolded; Semgrep rule bodies + test fixtures fill in per phase.

| Skill | Architecture touchpoint | Trigger | Required before |
|---|---|---|---|
| [`wal-durability-review`](../../.claude/skills/wal-durability-review/README.md) | Architecture Rule 4 + kill-9 contract | `internal/ingest/{wal,consumer}.go`; `.Sync()` / `.TruncateFront()` | Phase 7b — fix 3 WAL gaps from PR #14 |
| [`ratelimit-tuning-review`](../../.claude/skills/ratelimit-tuning-review/README.md) | Security #5 + CGNAT SLO | `internal/ratelimit/**`, `httprate` / `x/time/rate`, middleware chain | **Phase 10 SamplePlatform cutover — HARD GATE** |
| [`gdpr-code-review`](../../.claude/skills/gdpr-code-review/README.md) | Privacy Rules 1–9 + GDPR Art. 4(2), 17, 21 | `internal/identity/**`, `internal/audit/**`, `/api/privacy/*`, tracker JS | **Phase 11 public signup — HARD GATE** |
| [`dsar-completeness-checker`](../../.claude/skills/dsar-completeness-checker/README.md) | GDPR Art. 17 sink matrix | New migration + `erase.go` + audit sinks | **Phase 11 public signup — HARD GATE** (pair) |

### CGNAT reality (what the rate-limit skill enforces)

- **AS44244 Irancell** — ~1.3 M IPv4 for tens of millions of subscribers. A single public IPv4 fronts 5 000–10 000 concurrent subscribers at peak.
- **AS197207 MCI** — 828 IPv4 prefixes, no IPv6, entire mobile base.
- **AS57218 RighTel** — smaller but same NAT444 pattern.
- **100 req/s per-IP** throttles an entire apartment block the moment two neighbors load the homepage. Compound `(ip, site_id)` + ASN tier is the only viable shape.

### 3-phase schedule

- **Phase 7b (now)** — author Semgrep body for `wal-durability-review`; wire kill-9 CI gate; fix 3 WAL gaps surfaced in PR #14.
- **Phase 10 (SamplePlatform cutover)** — author ASN-tiered limiter in `internal/ratelimit/tier.go`; wire `iptoasn.com` TSV loader; ship k6 scenarios `normal` / `burst` / `ddos` / `cgnat`.
- **Phase 11 (SaaS public signup)** — author `gdpr-code-review` + `dsar-completeness-checker` Semgrep bodies; integration test enumerates `system.tables` dynamically; DPA draft committed at `docs/dpa-draft.md`; weekly rollup rebuild cron scheduled.

### Follow-ups (out of scope for the doc-27 install PR)

- Author `iptoasn.com` TSV loader in `internal/ratelimit/asn.go` (Phase 10).
- Draft DPA §X.Y at `docs/dpa-draft.md` using doc 27 §line 77-79 verbatim (Phase 11).
- Draft `docs/backup-retention.md` documenting backup rotation + erase propagation to next generation (Phase 11).
- Author `FIELDS.md` documenting all 34 EnrichedEvent fields with {purpose, retention, Article-6 basis} (Phase 11).
- Find a real PII-grep community skill to replace the missing `BehiSecc/sanitize` — check Snyk Feb 2026 curation, `BehiSecc/awesome-claude-skills` index, and `mshs01156/support-to-repro-pack` (closest; not adopted — scoped to bug-triage, not general PII grep).

---

## Doc 28 additions (close the final three gaps)

**Summary.** [`jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md`](../../../jaan-to/docs/research/28-geoip-iranian-dc-clickhouse.md) confirms **zero public Claude skill coverage** for three statnive-live surfaces (GeoIP pipeline correctness, Iranian DC deployment specifics, ClickHouse operational discipline). Builds four new custom skills, one material policy correction, and one roster correction.

### Policy correction — CC-BY-SA-4.0 carve-out for non-linked data files

Doc 28 §Gap 1 establishes that **every major free city-level GeoIP DB is CC-BY-SA-4.0** (IP2Location LITE DB23, IPinfo Lite, IPLocate free) and GeoLite2 additionally carries a MaxMind EULA that mandates auto-updates (air-gap-incompatible). The project's original strict `MIT/Apache/BSD/ISC` policy was **unsatisfiable** with any of these.

**Resolution** (shipped in [CLAUDE.md § License Rules](../../CLAUDE.md#license-rules-critical) — commit `4d26275`): carve-out amendment for *non-linked data files only*. GeoIP BIN databases are data, not linked code — the binary surface gate does not apply. Attribution is delivered in three surfaces (`LICENSE-third-party.md` + `/about` JSON + dashboard footer) enforced by the [`geoip-pipeline-review`](../../.claude/skills/geoip-pipeline-review/README.md) skill's Semgrep rule `geoip-attribution-string-present`.

**Alternative (Phase 10 SamplePlatform):** budget for paid IP2Location DB23 Site License — attribution waived per commercial terms. Price is sales-gated; comparable DBs range $99–$980/yr.

### Roster correction — ClickHouse/agent-skills is 1 skill, not 6

Historically the Install Summary table listed `clickhouse-agent-skills` as "6 of 6 (all)". That misrepresents the repo:

- **1 primary skill** — `clickhouse-best-practices` — ships 28 rules across 11 categories (Primary Key, Data Types, JOINs, Insert Batching, Mutation Avoidance, Partitioning, Skipping Indices, Materialized Views, Async Inserts, OPTIMIZE Avoidance, JSON).
- **4 auxiliary artifacts** — `chdb-datastore`, `chdb-sql`, `clickhousectl-cloud-deploy`, `clickhousectl-local-dev` — related but separate. Useful for embedded-CH unit tests (Phase 7) + local-dev setup.

All 5 directories stay installed. The summary row now reads "1 primary + 4 auxiliary" instead of "6 of 6 (all)".

### Custom skills catalog (doc 28 §gap-analysis)

Four `.claude/skills/*` directories scaffolded; Semgrep rule bodies + CI wiring fill in per phase.

| Skill | Architecture touchpoint | Trigger | Required before |
|---|---|---|---|
| [`iranian-dc-deploy`](../../.claude/skills/iranian-dc-deploy/README.md) | Isolation + Security #1 (TLS manual PEM) + OFAC 31 CFR 560.540(b)(3) | `deploy/**`, `ops/**`, `infra/**`, DNS zones, TLS/NTP/systemd, `*http.Client`, `internal/license/**` | **Weeks 21–24 SamplePlatform cutover — HARD GATE.** Blocks every SamplePlatform-destined PR after Week 20. |
| [`geoip-pipeline-review`](../../.claude/skills/geoip-pipeline-review/README.md) | Privacy Rule 1 (raw IP never persisted) + CC-BY-SA carve-out | `internal/enrich/geoip.go`, `**/*ip2location*`, `cmd/**/main.go`, `internal/about/**`, `LICENSE-third-party.md` | **Phase 10 SamplePlatform paid-DB23 cutover.** CC-BY-SA policy call Week 19 Day 1. |
| [`clickhouse-operations-review`](../../.claude/skills/clickhouse-operations-review/README.md) | Architecture Rules 1, 2, 4, 5 + operational defaults | `migrations/*.sql`, `internal/ingest/**`, `internal/query/**`, `config/ch*`, `prometheus/*.rules.yml`, `deploy/backup/**` | **Week 23 load-rehearsal.** Backup-restore drill + parts-ceiling gate. |
| [`clickhouse-upgrade-playbook`](../../.claude/skills/clickhouse-upgrade-playbook/README.md) | `{{if .Cluster}}` scope (DDL only, NOT data migration) | `migrations/*.sql`, `migrations/**/*.tmpl` with `Engine=` or `{{if .Cluster}}` | **P5 cluster upgrade.** Advisory only — no Semgrep rules. Paired with `clickhouse-operations-review`. |

### Anti-patterns (doc 28 §Anti-patterns)

Also mirrored in [CLAUDE.md § Anti-patterns](../../CLAUDE.md#anti-patterns-doc-28-anti-patterns--absolute-bans). Listed here for tooling review context:

- **No Cloudflare for any IR-resident code path** (OFAC + no IR POP).
- **No fsnotify for GeoIP reload** — overlayfs/NFS/kqueue silently lose events. SIGHUP only.
- **No `OPTIMIZE FINAL` "with careful review"** — OOMs 8c/32GB under merge pressure. Sanctioned alternative: `OPTIMIZE ... PARTITION '...' FINAL DEDUPLICATE` off-peak.
- **No phone-home license check "even for telemetry"** — OFAC interpretation of "services rendered" excludes commercial services to Iranian entities.
- **No AGPL linked into the binary; no CC-BY-SA except the carve-out for non-linked data files**. OS daemons (chrony, acme.sh, knot, BIND) are operator-installed → outside the boundary.
- **No ACME / Let's Encrypt from inside Iran** — issue outside, rsync PEM inward, SIGHUP swap.
- **`{{if .Cluster}}` is DDL templating only, NOT cluster-upgrade automation.** Data migration is manual via hard-link `ATTACH PARTITION`.

### 3-phase schedule (doc 28 §Full-optimization-roadmap)

- **Weeks 17–18 — `iranian-dc-deploy` first.** Highest dependency chain: blocks every SamplePlatform-destined PR after Week 20. DNS + TLS + blackout-sim CI must be green before any SamplePlatform-specific feature work lands.
- **Weeks 19–20 — `geoip-pipeline-review`.** Depends on `iranian-dc-deploy` (`airgap-update-geoip.sh` lives there). Block Phase 10 paid-DB23 cutover on green Semgrep + hot-reload integration + IP-leak log grep + attribution UI shipped. **CC-BY-SA policy resolution Week 19 Day 1.**
- **Weeks 20–22 (overlaps SamplePlatform rehearsal) — `clickhouse-operations-review` + `clickhouse-upgrade-playbook` paired.** Must be green before Week 23 load-rehearsal at 7K EPS. Backup-restore drill + parts-ceiling CI are the two gates SamplePlatform operations will watch.
- **Weeks 21–24 — SamplePlatform cutover.** Skills act as merge-gates. No custom-skill work during this window; fix bugs only.

### Follow-ups (out of scope for the doc-28 install PR)

- Fill in full Semgrep rule bodies (already written in `semgrep/rules.yaml` per skill; wire into `.github/workflows/*.yml`).
- Author `ops/cert-forge/` Hetzner box provisioning + ACME DNS-01 automation.
- Register `statnive.ir` + `.ایران` IDN bundle at IRNIC (Pars.ir or Gandi; US persons excluded from Gandi per T&Cs).
- Quote Asiatech IRR pricing across AT-VPS-B1 / AT-VPS-G2 / AT-VPS-A1 / dedicated 8c/32GB tiers.
- Quote paid IP2Location DB23 Site License for Phase 10 (SamplePlatform).
- Verify Bunny DNS AXFR-out support (likely unsupported; ClouDNS as AXFR primary instead).
- Place Ed25519 license-signing keypair on offline YubiKey in a non-US, non-Iran jurisdiction (operator decision).
- Decide MiravaOrg/Mirava licence (UNCONFIRMED); wrap functionality in-house if not permissive.
- **Phase 8 skill-roster review** — mid-Phase-8 checkpoint: re-validate each custom skill's trigger globs against real code before its Semgrep body lands. Triggers designed 8 weeks in advance often fire on the wrong globs once real code exists. Scope: all 14 custom skills, cross-check `globs:` in frontmatter vs actual file layout, remove the activation-preamble from any skill whose CI gate went green in the interim. Owner: TBD; blocks Weeks 19–22 body-authoring PRs.
