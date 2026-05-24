import { makeLazyCache, useLazyImport } from '../lib/lazy';

// Cally + atomico are ~13 KB gz. DatePicker ships in the initial JS chunk
// via AppShell, so eager-loading Cally would consume the bulk of the
// initial budget. Defer the import to the first time the Custom popover
// opens; the chunk is named via vite.config.ts `manualChunks: { cally }`
// and capped by `.size-limit.json`.

const cache = makeLazyCache<typeof import('cally')>();

export function useCallyReady(): boolean {
  return useLazyImport(cache, () => import('cally')) !== null;
}
