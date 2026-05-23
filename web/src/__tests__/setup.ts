// vitest setup — runs once per worker before any test file.
//
// matchMedia: ECharts and applyReducedMotion both call
// window.matchMedia at evaluation/render time. jsdom does not
// implement it, so a no-op stub keeps top-level imports from throwing.
if (typeof window !== 'undefined' && typeof window.matchMedia !== 'function') {
  const stub = (query: string): MediaQueryList => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  });
  window.matchMedia = stub as typeof window.matchMedia;
}

// ResizeObserver: ECharts' Chart.tsx wrapper observes its container.
// jsdom doesn't implement it; the no-op stub lets Chart.tsx mount
// without crashing in tests that mount it directly (Chart.test.tsx).
// Tests that mock LazyChart never hit this path.
if (typeof globalThis.ResizeObserver === 'undefined') {
  class NoopResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  globalThis.ResizeObserver = NoopResizeObserver as unknown as typeof ResizeObserver;
}
