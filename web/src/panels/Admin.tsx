import { useSignal } from '@preact/signals';
import { activeSiteSignal } from '../state/site';
import { SitesTab } from './admin/SitesTab';
import { UsersTab } from './admin/UsersTab';
import { GoalsTab } from './admin/GoalsTab';
import './Admin.css';

// Admin panel — single lazy chunk, tabbed between Sites + Users + Goals.
// Gated by role: App.tsx only routes here when userSignal.role === 'admin'.
//
// v1 keeps the UI deliberately simple. Inline forms, no modals, no
// pagination (admin-sized deployments have tens of rows per surface).
// Phase 11 SaaS adds cursor pagination + richer edit flows.
//
// The per-tab implementations live under ./admin/{Sites,Users,Goals}Tab.tsx
// so the three tabs can evolve independently without colliding on this file.

type Tab = 'sites' | 'users' | 'goals';

export default function Admin() {
  const tab = useSignal<Tab>('sites');
  const activeSite = activeSiteSignal.value;

  return (
    <section class="statnive-admin">
      {activeSite ? (
        <div class="statnive-admin-context" data-testid="admin-active-site">
          <strong>Managing site:</strong> {activeSite.hostname}
          {' '}<code>(site_id={activeSite.id})</code>
        </div>
      ) : null}

      <div class="statnive-admin-tabs" role="tablist">
        <button
          type="button"
          role="tab"
          aria-selected={tab.value === 'sites'}
          class={tab.value === 'sites' ? 'is-active' : ''}
          onClick={() => (tab.value = 'sites')}
        >
          Sites
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab.value === 'users'}
          class={tab.value === 'users' ? 'is-active' : ''}
          onClick={() => (tab.value = 'users')}
        >
          Users
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab.value === 'goals'}
          class={tab.value === 'goals' ? 'is-active' : ''}
          onClick={() => (tab.value = 'goals')}
        >
          Goals
        </button>
      </div>

      {tab.value === 'sites' ? <SitesTab /> : tab.value === 'users' ? <UsersTab /> : <GoalsTab />}
    </section>
  );
}
