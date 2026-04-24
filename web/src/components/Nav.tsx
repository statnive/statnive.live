import { hashSignal, navigate, type PanelName } from '../state/hash';
import { userSignal } from '../state/auth';
import { prefetchPanel } from './LazyPanel';
import { LivePulse } from './LivePulse';
import './Nav.css';

interface TabDef {
  id: PanelName | 'geo' | 'devices' | 'funnel';
  label: string;
  adminOnly?: boolean;
  soon?: boolean;
  live?: boolean;
}

// Hoisted outside component per `rendering-hoist-jsx` — static array
// identity lets Preact's diff skip list rebuilds on every render.
//
// SOON tabs (geo / devices / funnel) are visual-only — zero backend,
// zero route target, zero PanelName-union entry. Clicking is a no-op;
// parseHash() falls through to 'overview' if someone pastes `#geo`.
const TABS: ReadonlyArray<TabDef> = [
  { id: 'overview', label: 'Overview' },
  { id: 'sources', label: 'Sources' },
  { id: 'pages', label: 'Pages' },
  { id: 'seo', label: 'SEO' },
  { id: 'campaigns', label: 'Campaigns' },
  { id: 'realtime', label: 'Realtime', live: true },
  { id: 'admin', label: 'Admin', adminOnly: true },
  { id: 'geo', label: 'Geo', soon: true },
  { id: 'devices', label: 'Devices', soon: true },
  { id: 'funnel', label: 'Funnel', soon: true },
];

function isRealPanel(id: TabDef['id']): id is PanelName {
  return id !== 'geo' && id !== 'devices' && id !== 'funnel';
}

export function Nav() {
  const active = hashSignal.value.panel;
  const isAdmin = userSignal.value?.role === 'admin';
  const adminActive = active === 'admin';

  return (
    <nav class="statnive-nav" role="tablist">
      {TABS.filter((t) => !t.adminOnly || isAdmin).map((tab) => {
        if (tab.soon) {
          return (
            <span
              key={tab.id}
              role="presentation"
              aria-disabled="true"
              class="statnive-nav-tab is-soon"
            >
              <span class="tab-label">{tab.label}</span>
              <span class="tab-soon-pill">SOON</span>
            </span>
          );
        }

        const isActive = isRealPanel(tab.id) && tab.id === active;
        const classes = ['statnive-nav-tab'];
        if (isActive) classes.push('is-active');
        if (tab.id === 'admin' && adminActive) classes.push('is-active-admin');

        return (
          <a
            key={tab.id}
            role="tab"
            aria-selected={isActive}
            class={classes.join(' ')}
            href={'#' + tab.id}
            onMouseEnter={() => isRealPanel(tab.id) && prefetchPanel(tab.id)}
            onFocus={() => isRealPanel(tab.id) && prefetchPanel(tab.id)}
            onClick={(e) => {
              e.preventDefault();
              if (isRealPanel(tab.id)) {
                navigate(tab.id, hashSignal.value.params);
              }
            }}
          >
            <span class="tab-label">{tab.label}</span>
            {tab.live && isActive ? <LivePulse aria-label="Live" /> : null}
          </a>
        );
      })}
    </nav>
  );
}
