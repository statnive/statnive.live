# statnive.live tracker

First-party JavaScript tracker that fires pageviews + custom events at
`/api/event` on the analytics host. Vanilla JS, IIFE, **≤ 1.5 KB minified
/ ≤ 700 B gzipped**.

## Public API

```html
<script src="https://your-statnive-host/tracker.js" defer></script>
```

That's the entire install. After load, `window.statnive` exposes:

```js
statnive.track(name, props, value);   // custom event
statnive.identify(uid);               // raw uid; server hashes to SHA-256
```

`pageview` fires automatically on initial load and on every
`history.pushState` / `replaceState` / `popstate` (SPA route changes).

## Privacy contract (silently disables itself when…)

- `navigator.doNotTrack === '1'` (DNT)
- `navigator.globalPrivacyControl === true` (Sec-GPC)
- `navigator.webdriver === true` (Selenium / Playwright / headless Chrome)
- `window._phantom` / `window.callPhantom` (PhantomJS)

When disabled, both `track()` and `identify()` become no-ops. Your code
never throws; user opt-out is structural, not a banner.

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
| Minified | ≤ 1500 B | 1336 B |
| Gzipped | ≤ 700 B | 677 B |

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
