---
name: preact-signals-bundle-budget
description: MUST USE when editing `web/**` or `tracker/**`. Enforces dashboard bundle ~50KB min / ~15KB gz + tracker ~1.2KB min / ~600B gz via `size-limit`. Validates Preact + @preact/signals idioms (no React useState/useEffect in signal contexts), flags barrel imports, rejects >5KB deps, prevents CDN imports (reinforces air-gap).
license: MIT
metadata:
  author: statnive-live
  version: "0.1.0-scaffold"
  phase: 4
  research: "jaan-to/docs/research/25-ai-claude-skills-filimo-grade-analytics-platform.md §gap-analysis #5; doc 20 §frontend; CLAUDE.md §Stack"
---

# preact-signals-bundle-budget

> **Activation gate (Phase 4 tracker + Phase 5 dashboard).** This skill's Semgrep rule bodies and CI wiring are scheduled for Phase 4 (first tracker build, 1.2 KB / 600 B gz budget) and Phase 5 (first Preact component, 50 KB / 15 KB gz budget). Until the corresponding `.github/workflows/bundle-budget-gate.yml` is green on main, treat this skill as **advisory-only** — surface the checklist to the reviewer, do not block merges, and flag any mismatch as `activation-pending` rather than auto-fixing.

Fills the entire frontend-skill gap doc 25 calls out: "nothing targets Preact, `@preact/signals`, uPlot configuration, `go:embed`-hashed-asset workflow, or analytics-beacon/`sendBeacon` correctness." Both budgets and Preact-signals idioms live here.

## When this skill fires

- Any change under `web/src/**`, `web/vite.config.ts`, `web/package.json`, `web/vite.config.ts`.
- Any change under `tracker/src/**`, `tracker/rollup.config.js`, `tracker/package.json`.
- Any new `import` from `react`, `react-dom`, `shadcn`, `radix-ui`, `@mui/*`, `antd`, etc.
- Any `<link href="https://...">` or `fetch('https://...')` in frontend code.

## Enforced invariants

### Bundle budgets (non-negotiable)

| Bundle | Min | Gzip | Tool |
|---|---|---|---|
| Dashboard SPA (`web/dist/*.js`) | ≤ 50 KB | ≤ 15 KB | `size-limit` |
| Tracker IIFE (`tracker/dist/statnive.min.js`) | ≤ 1.2 KB | ≤ 600 B | `size-limit` |

Violations fail CI. No budget-overrides without an approved PLAN.md v1.1 update.

### Preact + signals idioms

1. State lives in signals (`@preact/signals-core` / `@preact/signals`), not `useState`. Pass `{signal}` directly into JSX, **not** `{signal.value}`, to bind to DOM text nodes without re-renders.
2. `useEffect` is acceptable for imperative DOM setup (uPlot / Frappe mount) but never for state derivation — derived state is `computed()`.
3. No React imports (`react`, `react-dom`, `react-router`, `@tanstack/react-*` unless explicitly Preact-compatible).
4. `import { Component } from 'preact'` is rarely needed — prefer functional components.
5. File-level barrel imports (`import { ... } from './components'`) are rejected — tree-shaking breaks for Rollup + Vite in some shapes. Use direct-path imports.

### Air-gap reinforcement

1. No CDN hosts in `web/index.html`, `web/public/**`, or `tracker/src/**`. Pairs with `air-gap-validator`.
2. Fonts are self-hosted (`web/public/fonts/*.woff2`), not `fonts.googleapis.com`.
3. uPlot, Frappe Charts, Preact, @preact/signals are bundled via `go:embed`, not script-tag imports.

### Tracker-specific (tracker/)

1. Transport: `navigator.sendBeacon` first, `fetch(…, {keepalive: true})` fallback. No `XMLHttpRequest`.
2. Content-Type `text/plain` for CORS simplicity.
3. No cookies read/written by the tracker itself (cookies are set server-side on first visit; the tracker transmits via header only).
4. Graceful degradation: skip cleanly on Brave / Firefox tracking-protection without errors.
5. No third-party requests.

## Should trigger (reject)

```tsx
// BAD — React useState in signal context, re-renders on every change
import { useState, useEffect } from 'react';
function Overview() {
  const [count, setCount] = useState(0);
  useEffect(() => { fetch('https://api.external.com/stats').then(r => setCount(r.json())); }, []);
  return <div>{count}</div>;
}
```

```ts
// BAD — barrel import, CDN fetch
import { SignalBarrel } from '@/components';
const Uplot = await import('https://cdn.jsdelivr.net/npm/uplot');
```

## Should NOT trigger (allow)

```tsx
import { signal, computed } from '@preact/signals';
import uPlot from 'uplot';  // bundled via go:embed

const visitors = signal(0);
const formatted = computed(() => visitors.value.toLocaleString('en-US'));

export function Overview() {
  return <div>{formatted}</div>;  // NOT {formatted.value}
}
```

## Implementation (TODO — Phase 4 for tracker, Phase 5 for dashboard)

- `size-limit.json` — TODO: two entries (dashboard 15 KB-gz / 50 KB-min, tracker 600 B-gz / 1.2 KB-min).
- `eslint-rules/no-react-in-signal.js` — TODO: custom rule flagging `useState`/`useEffect` co-use with signals.
- `eslint-rules/no-barrel-import.js` — TODO: flag `../components` index imports.
- `eslint-rules/no-cdn.js` — TODO: flag `https://` URLs in source (pairs with `air-gap-validator`).
- `test/fixtures/` — TODO: should-trigger / should-not-trigger cases.

Full spec: [README.md](README.md).