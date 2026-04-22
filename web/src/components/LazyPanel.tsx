import type { ComponentType } from 'preact';
import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { Loader } from './Loader';

// LazyPanel dynamic-imports a panel module by name and renders its
// default export. Each non-Overview panel ships in its own chunk per
// `bundle-dynamic-imports` — Overview stays static so first paint has
// no network dependency beyond the initial JS bundle.
//
// Resolved modules are cached in a module-level Map so tab re-entry is
// synchronous (no flicker + no re-fetch of an already-loaded chunk).
// Prefetching on hover is wired in Nav.tsx: call prefetchPanel(name) to
// warm the cache before the user actually clicks.

type LazyModule = { default: ComponentType };

const cache = new Map<string, ComponentType>();

function loaderFor(name: string): () => Promise<LazyModule> {
  switch (name) {
    case 'sources':
      return () => import('../panels/Sources');
    case 'pages':
      return () => import('../panels/Pages');
    case 'seo':
      return () => import('../panels/SEO');
    case 'campaigns':
      return () => import('../panels/Campaigns');
    case 'realtime':
      return () => import('../panels/Realtime');
    default:
      // Unknown panels fall through to a loader that resolves to
      // <Loader /> — callers should never pass an unknown name since
      // the hashSignal router narrows to the PanelName union first.
      return () => Promise.resolve({ default: Loader });
  }
}

export function prefetchPanel(name: string): void {
  if (cache.has(name)) return;
  void loaderFor(name)().then((mod) => {
    cache.set(name, mod.default);
  });
}

export function LazyPanel({ name }: { name: string }) {
  const comp = useSignal<ComponentType | null>(cache.get(name) ?? null);
  const err = useSignal<string | null>(null);

  useEffect(() => {
    if (cache.has(name)) {
      comp.value = cache.get(name) ?? null;
      return;
    }

    let cancelled = false;

    loaderFor(name)()
      .then((mod) => {
        cache.set(name, mod.default);
        if (!cancelled) comp.value = mod.default;
      })
      .catch((e: unknown) => {
        if (!cancelled) err.value = e instanceof Error ? e.message : String(e);
      });

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name]);

  if (err.value) {
    return (
      <section class="statnive-section">
        <p class="statnive-error">could not load panel — see logs</p>
      </section>
    );
  }

  const C = comp.value;
  if (!C) {
    return <Loader />;
  }

  return <C />;
}
