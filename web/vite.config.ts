import { defineConfig } from 'vite';
import preact from '@preact/preset-vite';

// statnive-live SPA build.
//
// `base: '/app/'` so the binary can serve from /app/* via go:embed without
// rewriting asset URLs. Hashed filenames so the long-cache headers in
// internal/dashboard/spa/dashboard.go are safe.
export default defineConfig({
  plugins: [preact()],
  base: '/app/',
  build: {
    // Write output to the Go package directory so `//go:embed` (which
    // forbids `..` paths inside the pattern) picks it up. Same pattern
    // as tracker/rollup.config.mjs → internal/tracker/dist/tracker.js.
    outDir: '../internal/dashboard/spa/dist',
    assetsDir: 'assets',
    emptyOutDir: true,
    sourcemap: false,
    minify: 'esbuild',
    target: 'es2020',
    // Never inline assets as base64 data URIs — our CSP `font-src 'self'`
    // + `img-src 'self' data:` combination allows data: URIs for images
    // only, not fonts. Vite's default 4 KB inline threshold would bundle
    // small font subsets as `data:font/woff2;base64,...` which the
    // browser then refuses per CSP. Forcing every asset to ship as a
    // separate hashed file in /app/assets/ keeps them inside font-src.
    assetsInlineLimit: 0,
    rollupOptions: {
      output: {
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash][extname]',
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    // e2e/ holds Playwright specs driven via `npm run e2e`. Vitest must
    // ignore them — @playwright/test doesn't work under Vitest's harness.
    exclude: ['node_modules/**', 'dist/**', 'e2e/**'],
  },
});
