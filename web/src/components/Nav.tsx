import { hashSignal, navigate, type PanelName } from '../state/hash';
import { prefetchPanel } from './LazyPanel';
import './Nav.css';

// Hoisted outside component per `rendering-hoist-jsx` — static array
// identity lets Preact's diff skip list rebuilds on every render.
const TABS: ReadonlyArray<{ id: PanelName; label: string }> = [
  { id: 'overview', label: 'Overview' },
  { id: 'sources', label: 'Sources' },
  { id: 'pages', label: 'Pages' },
  { id: 'seo', label: 'SEO' },
  { id: 'campaigns', label: 'Campaigns' },
  { id: 'realtime', label: 'Realtime' },
];

export function Nav() {
  const active = hashSignal.value.panel;

  return (
    <nav class="statnive-nav" role="tablist">
      {TABS.map((tab) => (
        <a
          key={tab.id}
          role="tab"
          aria-selected={tab.id === active}
          class={'statnive-nav-tab' + (tab.id === active ? ' is-active' : '')}
          href={'#' + tab.id}
          onMouseEnter={() => prefetchPanel(tab.id)}
          onFocus={() => prefetchPanel(tab.id)}
          onClick={(e) => {
            e.preventDefault();
            navigate(tab.id, hashSignal.value.params);
          }}
        >
          {tab.label}
        </a>
      ))}
    </nav>
  );
}
