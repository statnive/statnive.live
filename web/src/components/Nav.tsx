import { hashSignal, navigate, type PanelName } from '../state/hash';
import { userSignal } from '../state/auth';
import { prefetchPanel } from './LazyPanel';
import './Nav.css';

// Hoisted outside component per `rendering-hoist-jsx` — static array
// identity lets Preact's diff skip list rebuilds on every render.
const TABS: ReadonlyArray<{ id: PanelName; label: string; adminOnly?: boolean }> = [
  { id: 'overview', label: 'Overview' },
  { id: 'sources', label: 'Sources' },
  { id: 'pages', label: 'Pages' },
  { id: 'seo', label: 'SEO' },
  { id: 'campaigns', label: 'Campaigns' },
  { id: 'realtime', label: 'Realtime' },
  { id: 'admin', label: 'Admin', adminOnly: true },
];

export function Nav() {
  const active = hashSignal.value.panel;
  const isAdmin = userSignal.value?.role === 'admin';

  return (
    <nav class="statnive-nav" role="tablist">
      {TABS.filter((t) => !t.adminOnly || isAdmin).map((tab) => (
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
