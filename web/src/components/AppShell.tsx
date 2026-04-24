import type { ComponentChildren } from 'preact';
import { Nav } from './Nav';
import { DatePicker } from './DatePicker';
import { FilterPanel } from './FilterPanel';
import { SiteSwitcher } from './SiteSwitcher';
import { userSignal } from '../state/auth';
import './AppShell.css';

// AppShell is the sticky four-row chrome stack that wraps every
// authenticated panel: TopBar → DateBar → TabBar → FilterStrip.
// Pure layout + visual wrapping — every piece of state lives on the
// existing components (SiteSwitcher / DatePicker / FilterPanel / Nav).
// Phase 5e visual slice; replaces the flat header that lived in App.tsx.
export interface AppShellProps {
  children?: ComponentChildren;
  onLogout?: (ev: Event) => void | Promise<void>;
}

const TZ_LABEL = 'Tehran (UTC+03:30)';

export function AppShell({ children, onLogout }: AppShellProps) {
  const user = userSignal.value;

  return (
    <div class="statnive-shell">
      <header class="statnive-topbar" role="banner">
        <h1 class="statnive-wordmark">
          statnive<em class="statnive-wordmark-live">.live</em>
        </h1>
        <div class="statnive-topbar-right">
          <SiteSwitcher />
          {user ? (
            <span class="statnive-user-chip" aria-label="signed-in user">
              <span class="statnive-user-chip-name">{user.username ?? user.email}</span>
              <span class="statnive-user-chip-role">{user.role}</span>
            </span>
          ) : null}
          {user && onLogout ? (
            <button
              type="button"
              class="statnive-logout"
              onClick={onLogout}
              aria-label="Sign out"
            >
              Sign out
            </button>
          ) : null}
        </div>
      </header>

      <div class="statnive-datebar" role="region" aria-label="Date range">
        <DatePicker />
        <span class="statnive-tz-chip" aria-label="timezone">{TZ_LABEL}</span>
      </div>

      <Nav />

      <FilterPanel />

      <main class="statnive-main">{children}</main>
    </div>
  );
}
