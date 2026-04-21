# air-gap-validator — full spec

## Architecture rule

Encodes **CLAUDE.md Isolation block** (lines 69-86) and **Project Goal 5**. Matches the **PLAN.md Air-Gapped / Isolated Deployment** operator playbook (PLAN.md §Air-Gapped / Isolated Deployment).

## Research anchors

- [jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md](../../../../jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md) §gap-analysis #2.
- [jaan-to/docs/research/14-data-high-performance-standalone-analytics-architecture.md](../../../../jaan-to/docs/research/14-data-high-performance-standalone-analytics-architecture.md) §air-gap.
- [jaan-to/docs/research/20-dev-go-clickhouse-analytics-implementation-blueprint.md](../../../../jaan-to/docs/research/20-dev-go-clickhouse-analytics-implementation-blueprint.md) §deploy.

## Implementation phase

**Phase 0 — Project Setup.** Skill must exist before the first dependency is added. Air-gap acceptance test (PLAN.md Verification item #17) becomes the gate for every release.

## Files

- `checks/outbound-denylist.yml` — TODO: regex patterns for forbidden external hosts.
- `checks/import-denylist.yml` — TODO: Go imports that commonly pull in auto-updaters.
- `scripts/airgap-acceptance.sh` — TODO: wraps iptables drop-all + integration test.
- `test/fixtures/should-trigger/` — TODO: outbound init, CDN URL, un-gated network feature.
- `test/fixtures/should-not-trigger/` — TODO: opt-in config-gated feature, go:embed assets, loopback-only ClickHouse.

## Denylist seed (outbound hosts)

- `fonts.googleapis.com`, `fonts.gstatic.com`
- `cdn.jsdelivr.net`, `unpkg.com`, `cdnjs.cloudflare.com`, `ga.jspm.io`
- `www.google-analytics.com`, `google-analytics.com`, `plausible.io`, `pirsch.io`, `matomo.cloud`
- `api.telegram.org` (unless `telegram.enabled: true`)
- `acme-v02.api.letsencrypt.org` (unless `tls.autocert_enabled: true` — v1.1)
- `api.github.com`, `objects.githubusercontent.com`

## Import denylist seed

- `golang.org/x/crypto/acme/autocert` — allowed only if `tls.autocert_enabled: true` (v1.1); forbidden at v1 (manual PEM files only per CLAUDE.md §Security #1).
- auto-update SDKs: `github.com/tj/go-update`, `github.com/inconshreveable/go-update`, `github.com/creativeprojects/go-selfupdate`.
- Telemetry SDKs: `github.com/getsentry/sentry-go`, `github.com/newrelic/go-agent`, `github.com/DataDog/datadog-go` — allowed only as opt-in.

## CI integration (TODO)

```makefile
airgap-static:
    ./.claude/skills/air-gap-validator/scripts/airgap-static.sh

airgap-acceptance:
    ./.claude/skills/air-gap-validator/scripts/airgap-acceptance.sh

release-gate: airgap-static airgap-acceptance
    $(MAKE) test-integration
```

## Scope

Applies to the full repo. The only exception is opt-in config-gated external services listed in CLAUDE.md §Isolation (lines 73-85) and PLAN.md §Opt-in external services.

## What does NOT trigger this skill

- Hetzner MCP server calls (operator tooling, lives in operator's Claude Code, not the binary).
- Context7 MCP docs fetches (build-time only; documented in `docs/tech-docs/`).
- `go mod download` during build — happens once on the build host, not at runtime.
