# statnive.live tracker

First-party JavaScript tracker that fires pageviews + custom events at
`/api/event` on the analytics host. Vanilla JS, IIFE, **≤ 1.5 KB minified
/ ≤ 750 B gzipped**.

## Public API

```html
<script src="https://your-statnive-host/tracker.js" defer></script>
```

That's the entire install. After load, `window.statniveLive` exposes:

```js
statniveLive.track(name, props, value);   // custom event
statniveLive.identify(uid);               // raw uid; server hashes to SHA-256
```

The namespace is `statniveLive` (matching the product domain `statnive.live`)
not `statnive`. This avoids collisions with the unrelated WP plugin tracker
(<https://wordpress.org/plugins/statnive>) that some same-brand customers
also load — it installs its own `window.statnive` queue stub. Two products,
two namespaces. See [LEARN.md Lesson 31](../LEARN.md#lesson-31).

`pageview` fires automatically on initial load and on every
`history.pushState` / `replaceState` / `popstate` (SPA route changes).

## Privacy contract (silently disables itself when…)

- `navigator.webdriver === true` (Selenium / Playwright / headless Chrome)
- `window._phantom` / `window.callPhantom` (PhantomJS)

These are anti-automation guards, not privacy policy. When triggered,
both `track()` and `identify()` become no-ops; your code never throws.

**`DNT: 1` and `Sec-GPC: 1` are NOT honored client-side.** Browsers attach
those headers automatically on every request; the binary honors them
server-side only when the operator has set `consent.respect_dnt: true` /
`consent.respect_gpc: true` in their YAML config (default false). The
previous client-side short-circuit was hiding 70-85% of legitimate Brave
/ Firefox-strict / Safari traffic from operator dashboards (see LEARN.md
Lesson 24); the policy decision now lives in the binary where each
operator can configure it for their jurisdiction.

## What it does NOT do

- ❌ no cookies, localStorage, sessionStorage, IndexedDB
- ❌ no fingerprinting (canvas, WebGL, font-enum, `navigator.plugins`,
  `AudioContext`, `deviceMemory`, `hardwareConcurrency`)
- ❌ no `XMLHttpRequest` (sendBeacon + fetch keepalive only)
- ❌ no third-party scripts; no external CDN

## Build + test

```bash
cd tracker
npm ci
npm test         # Vitest + jsdom (15 specs)
npm run build    # → ../internal/tracker/dist/tracker.js
```

The built artifact lives in the Go package directory because `go:embed`
forbids `..` paths. The Go binary embeds the file at compile time.

## Bundle budget gate

Both [`internal/tracker/tracker_test.go`](../internal/tracker/tracker_test.go)
(Go-side, in-process) and `make tracker-size` (shell, post-build) assert:

| Metric | Budget | Current |
|---|---:|---:|
| Minified | ≤ 1500 B | 1478 B |
| Gzipped | ≤ 750 B | 747 B |

`make audit` chains `tracker-size` so every PR fails fast if the bundle
regresses.

## Endpoint configuration

The script tag can override the default `/api/event`:

```html
<script src="/tracker.js" data-statnive-endpoint="/custom/event" defer></script>
```

Useful when the analytics binary lives at a non-root path or when
proxying through a CDN edge.

## Why vanilla JS?

The SamplePlatform CDN budget for tracker bytes is the real constraint
(CLAUDE.md tracker spec). React, Preact, or any framework would
balloon past 50 KB before your code ran. Vanilla + sendBeacon +
history-API patches give us the entire feature surface in 1.3 KB.
