---
title: Vite Documentation
library_id: /websites/vite_dev
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: preact-jsx-config
tags: [context7, vite, preact, typescript, build]
source: Context7 MCP
cache_ttl: 7 days
---

# Vite — Preact + TypeScript config (confirmed)

## ⚠️ API DELTA: `build.rollupOptions` → `build.rolldownOptions`

Vite has migrated from Rollup to **Rolldown** (Rust-based) as its underlying bundler. The old option is deprecated:

```js
// OLD (deprecated alias)
export default defineConfig({
  build: {
    rollupOptions: { /* … */ }
  }
})

// NEW (preferred)
export default defineConfig({
  build: {
    rolldownOptions: { /* https://rolldown.rs/reference/ */ }
  }
})
```

**Action for statnive-live `web/vite.config.ts`:** Use `rolldownOptions` from day 1. This is a new deprecation that should be reflected in the project setup.

## JSX importSource for Preact

```js
// vite.config.ts
import { defineConfig } from 'vite'

export default defineConfig({
  oxc: {
    jsx: {
      importSource: 'preact',
    },
  },
})
```

**Note:** Vite now uses **oxc** (Rust-based) instead of esbuild for JSX transformation. The `esbuild.jsxImportSource` option is superseded by `oxc.jsx.importSource`.

## Dev server proxy for API (dashboard → Go backend)

```js
server: {
  proxy: {
    '/api': {
      target: 'http://localhost:8080',
      changeOrigin: true,
    },
  },
}
```

## Middleware mode + WS proxy (if embedding dev into Go server)

```ts
const vite = await createServer({
  server: {
    middlewareMode: { server: parentServer },
    proxy: {
      '/ws': { target: 'ws://localhost:8080', ws: true },
    },
  },
})
```

## License: MIT

## 🔴 API delta summary (since 2026-04-17 snapshot)

- `build.rollupOptions` → `build.rolldownOptions` (rollupOptions still works as alias, will be removed)
- JSX config lives under `oxc.jsx.*`, not `esbuild.*`
