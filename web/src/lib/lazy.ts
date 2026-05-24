import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';

// Shared lazy-import primitive. Caller passes a stable cache object and a
// loader; the hook dedupes parallel calls via the cache's `promise` slot
// and reflects readiness through a signal. Used by LazyChart (ECharts)
// and LazyCally (Cally web components) so the Preact + size-limit
// chunking story stays uniform.

export interface LazyCache<T> {
  value: T | null;
  promise: Promise<T> | null;
}

export function makeLazyCache<T>(): LazyCache<T> {
  return { value: null, promise: null };
}

export function useLazyImport<T>(
  cache: LazyCache<T>,
  loader: () => Promise<T>,
): T | null {
  const v = useSignal<T | null>(cache.value);
  useEffect(() => {
    if (cache.value) {
      v.value = cache.value;
      return;
    }
    let cancelled = false;
    cache.promise ??= loader();
    void cache.promise.then((mod) => {
      cache.value = mod;
      if (!cancelled) v.value = mod;
    });
    return () => {
      cancelled = true;
    };
  }, []);
  return v.value;
}
