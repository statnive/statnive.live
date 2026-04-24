import { Overview } from './panels/Overview';
import { AppShell } from './components/AppShell';
import { LazyPanel } from './components/LazyPanel';
import { Login } from './pages/Login';
import {
  authCheckedSignal,
  authSignal,
  logout,
  userSignal,
} from './state/auth';
import { hashSignal } from './state/hash';
import './App.css';

// Only Overview is statically imported — every other panel ships in its
// own chunk via LazyPanel per `bundle-dynamic-imports`. Keeps initial
// JS small (Overview is the default landing panel, so no waterfall) and
// caps any single panel's weight against the overall 14 KB gz budget.
function renderPanel() {
  switch (hashSignal.value.panel) {
    case 'overview':
      return <Overview />;
    case 'sources':
      return <LazyPanel name="sources" />;
    case 'pages':
      return <LazyPanel name="pages" />;
    case 'seo':
      return <LazyPanel name="seo" />;
    case 'campaigns':
      return <LazyPanel name="campaigns" />;
    case 'realtime':
      return <LazyPanel name="realtime" />;
    case 'admin':
      return <LazyPanel name="admin" />;
    default:
      return <Overview />;
  }
}

async function onLogout(ev: Event) {
  ev.preventDefault();
  await logout();
}

export function App() {
  // Auth-gate: if /api/user hasn't resolved yet, render nothing (prevents
  // a Login/Dashboard flash on each reload). Once resolved, show Login
  // if unauthenticated, the dashboard otherwise.
  if (!authCheckedSignal.value) return null;

  // CI bearer-token path preserves the pre-Phase-2b behavior: when the
  // meta tag has a value, the SPA treats it as authenticated without
  // requiring a successful /api/user round-trip.
  const authenticated = userSignal.value != null || authSignal.value !== '';

  if (!authenticated) {
    return <Login />;
  }

  return <AppShell onLogout={onLogout}>{renderPanel()}</AppShell>;
}
