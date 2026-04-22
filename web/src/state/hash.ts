import { signal } from '@preact/signals';

// Panel identifiers match the URL hash. Unknown / empty hash falls back
// to 'overview' (default landing). Keep in sync with App.tsx's switch
// and Nav.tsx's tab list — single source of truth for valid panel names
// is the PanelName union.
export type PanelName =
  | 'overview'
  | 'sources'
  | 'pages'
  | 'seo'
  | 'campaigns'
  | 'realtime';

export const DEFAULT_PANEL: PanelName = 'overview';

const VALID: ReadonlySet<string> = new Set<PanelName>([
  'overview',
  'sources',
  'pages',
  'seo',
  'campaigns',
  'realtime',
]);

export interface HashState {
  panel: PanelName;
  params: URLSearchParams;
}

function parseHash(hash: string): HashState {
  // location.hash is '#panel?a=1&b=2' or '#panel' or '' — strip the '#'
  // then split on the first '?'.
  const raw = hash.startsWith('#') ? hash.slice(1) : hash;
  const qIdx = raw.indexOf('?');
  const panelPart = qIdx === -1 ? raw : raw.slice(0, qIdx);
  const queryPart = qIdx === -1 ? '' : raw.slice(qIdx + 1);

  const panel = VALID.has(panelPart) ? (panelPart as PanelName) : DEFAULT_PANEL;

  return { panel, params: new URLSearchParams(queryPart) };
}

// Serialize back to 'panel?k=v&k2=v2' or just 'panel' when empty.
function serialize(state: HashState): string {
  const q = state.params.toString();
  return q ? `${state.panel}?${q}` : state.panel;
}

export const hashSignal = signal<HashState>(parseHash(
  typeof window === 'undefined' ? '' : window.location.hash,
));

// navigate mutates the URL hash WITHOUT triggering a scroll-to-top jump
// (history.replaceState rather than setting location.hash directly). The
// 'hashchange' listener below then updates hashSignal — same code path
// whether the user clicks a link or another component calls navigate.
export function navigate(panel: PanelName, params?: URLSearchParams): void {
  const next: HashState = {
    panel,
    params: params ?? new URLSearchParams(),
  };
  const hash = '#' + serialize(next);
  if (typeof window !== 'undefined' && window.location.hash !== hash) {
    window.history.replaceState(null, '', hash);
  }
  hashSignal.value = next;
}

// Install a single hashchange listener at module load — covers both
// user-initiated hash changes (browser back/forward, link clicks) and
// programmatic history.pushState from navigate().
if (typeof window !== 'undefined') {
  window.addEventListener('hashchange', () => {
    hashSignal.value = parseHash(window.location.hash);
  });
}

// parseHash exported for tests (router.test.ts asserts fallback logic).
export { parseHash };
