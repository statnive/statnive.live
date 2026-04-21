# preact-signals-bundle-budget — full spec

## Architecture rule

Encodes **CLAUDE.md Stack** (line 18 — "Preact + @preact/signals + uPlot + Frappe Charts (~50KB minified / ~15KB gzipped), embedded via go:embed") and (line 19 — "Tracker: Vanilla JS ~1.2KB minified / ~600B gzipped"). Cross-references the **Isolation** rule (tracker must be first-party, no CDN).

## Research anchors

- [jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md](../../../../jaan-to/docs/research/25-ai-claude-skills-SamplePlatform-grade-analytics-platform.md) §gap-analysis #5.
- [jaan-to/docs/research/20-dev-go-clickhouse-analytics-implementation-blueprint.md](../../../../jaan-to/docs/research/20-dev-go-clickhouse-analytics-implementation-blueprint.md) §frontend + §tracker.
- [jaan-to/docs/research/21-microsoft-clarity-codebase-review-patterns-for-self-hosted-analytics.md](../../../../jaan-to/docs/research/21-microsoft-clarity-codebase-review-patterns-for-self-hosted-analytics.md) §root-domain cookie walking.
- `docs/tech-docs/preact-signals.md` — project-cached Context7 snapshot (note: `{signal}` vs `{signal.value}` is the zero-rerender trick).

## Implementation phase

- **Phase 4 — Tracker JS** (Week 10): tracker budget becomes load-bearing on day 1.
- **Phase 5 — Dashboard Frontend** (Weeks 11-13): dashboard budget becomes load-bearing with the first Preact component.

## Files

- `size-limit.json` — TODO: two bundle entries + CI runner.
- `eslint-rules/` — TODO: three custom rules (no-react-in-signal, no-barrel-import, no-cdn).
- `test/fixtures/` — TODO: should-trigger / should-not-trigger tsx + rollup output cases.

## Pairs with

- `air-gap-validator` — owns the CDN ban; this skill duplicates a narrow frontend check because the air-gap one is Go-focused.
- `vercel-labs/web-design-guidelines` (community install) — owns accessibility, focus, forms, animation UI rules. This skill owns Preact/bundle/tracker specifics.
- `knip-unused-code-dependency-finder` (community install) — feeds this skill by surfacing unused deps, which this skill then gates on.

## CI integration (TODO)

```jsonc
// web/package.json
{
  "scripts": {
    "bundle-gate": "size-limit"
  },
  "size-limit": [
    { "path": "dist/statnive-spa.js", "limit": "15 KB" },
    { "path": "../tracker/dist/statnive.min.js", "limit": "600 B" }
  ]
}
```

```makefile
bundle-gate:
    npm --prefix web run bundle-gate

release-gate: lint test test-integration bundle-gate airgap-acceptance
```

## uPlot-specific guidance (not trigger rules)

- One `uPlot.sync()` cursor group across all time-series charts on a panel (docs 20 §frontend).
- Pre-allocate series arrays (`uPlot` is picky about mutating series after init).
- Defer chart init to `useEffect` with empty deps — re-init on data change, not hook order.
- Always pass `width` / `height` explicitly; uPlot does not size to container.

Guidance only — the skill validates bundle budget + signals idioms, not uPlot config correctness. A future skill can expand here if uPlot bugs become a recurring class.

## Scope

- `web/**` (dashboard SPA).
- `tracker/**` (vanilla JS tracker).
- `web/package.json` and `tracker/package.json` dep additions.
- Does **not** apply to `docs/tech-docs/**` (build-time reference docs) or `internal/**` (Go code).
