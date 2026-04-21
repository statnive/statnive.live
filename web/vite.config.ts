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
  },
});
