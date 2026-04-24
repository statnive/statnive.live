import { render } from 'preact';
import './fonts.css';
import './tokens.css';
import './reset.css';
import { App } from './App';
import { authCheckedSignal, authSignal, loadCurrentUser } from './state/auth';

// Phase 2b — bootstrap the current user before first render so the
// App component can render Login vs. dashboard deterministically
// without a Login flash on reload.
//
// Two paths:
//   1. Cookie auth (prod) — fetch /api/user; populates userSignal on 200.
//   2. Legacy bearer (CI/smoke) — authSignal starts non-empty from the
//      meta tag injected by internal/dashboard/spa/dashboard.go, which
//      the App gate treats as authenticated without requiring a round-trip.
if (authSignal.value) {
  authCheckedSignal.value = true;
} else {
  void loadCurrentUser();
}

const root = document.getElementById('statnive-app');
if (root) {
  render(<App />, root);
}
