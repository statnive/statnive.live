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
  | 'realtime'
  | 'admin';

export const DEFAULT_PANEL: PanelName = 'overview';

const VALID: ReadonlySet<string> = new Set<PanelName>([
  'overview',
  'sources',
  'pages',
  'seo',
  'campaigns',
  'realtime',
  'admin',
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

// navigate mutates the URL hash via pushState so browser back / forward
// buttons walk the panel history as operators expect. Setting
// location.hash directly would achieve the same but also scroll to the
// element with id = hash; pushState doesn't trigger that. Filter /
// date-picker changes that REPLACE the same panel's params call
// replaceHashParams instead to avoid polluting history.
export function navigate(panel: PanelName, params?: URLSearchParams): void {
  const next: HashState = {
    panel,
    params: params ?? new URLSearchParams(),
  };
  const hash = '#' + serialize(next);
  if (typeof window !== 'undefined' && window.location.hash !== hash) {
    window.history.pushState(null, '', hash);
  }
  hashSignal.value = next;
}

// replaceHashParams updates the URL hash's params without adding a
// history entry. Used by FilterPanel / DatePicker where "click a chip"
// should not create a back-button stop.
export function replaceHashParams(params: URLSearchParams): void {
  const next: HashState = { panel: hashSignal.value.panel, params };
  const hash = '#' + serialize(next);
  if (typeof window !== 'undefined' && window.location.hash !== hash) {
    window.history.replaceState(null, '', hash);
  }
  hashSignal.value = next;
}

// hashchange fires on location.hash = ... and on browser back/forward
// between pushed states. popstate fires on back/forward. Both keep
// hashSignal in sync with the URL.
if (typeof window !== 'undefined') {
  const resync = () => {
    hashSignal.value = parseHash(window.location.hash);
  };
  window.addEventListener('hashchange', resync);
  window.addEventListener('popstate', resync);
}

// parseHash exported for tests (router.test.ts asserts fallback logic).
export { parseHash };
