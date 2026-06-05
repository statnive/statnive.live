import { useState } from 'preact/hooks';
import { loadCurrentUser, userSignal, authSignal } from '../state/auth';
import './Login.css';

// safeReturnTo reads ?return_to from `search` and returns it ONLY if it is a
// same-origin relative path. The OAuth AS appends it when it bounces an
// unauthenticated /authorize to the login page (LoginPath?return_to=/authorize?
// ...), so login resumes the consent flow. Open-redirect guard: accept a single
// leading "/" but reject "//" and "/\" (browsers treat both as protocol-relative
// → external origin) and any value the URL parser resolves to a different
// origin. Empty string ⇒ fall through to the normal dashboard. Pure (takes
// search + origin) so the guard is unit-testable without DOM stubbing.
export function safeReturnTo(search: string, origin: string): string {
  const raw = new URLSearchParams(search).get('return_to') ?? '';
  if (!raw.startsWith('/') || raw.startsWith('//') || raw.startsWith('/\\')) return '';

  // Defence in depth: resolve against the origin and confirm it stays
  // same-origin (catches odd encodings the prefix check might miss).
  try {
    const u = new URL(raw, origin);
    if (u.origin !== origin) return '';
    return u.pathname + u.search;
  } catch {
    return '';
  }
}

// Login is the unauthenticated-route landing page. Submits {email, password}
// to POST /api/login; on success the server sets the HttpOnly session
// cookie and we reload the current user into userSignal. The server
// returns a uniform "invalid credentials" body on every failure path
// (unknown user / wrong password / disabled / rate-limit / lockout) so
// the UI never needs to distinguish — single "invalid credentials" message
// is the right UX anyway.
export function Login(props: { demoBanner?: string }) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string>('');
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(ev: Event) {
    ev.preventDefault();
    if (submitting) return;
    setSubmitting(true);
    setError('');

    try {
      const res = await fetch('/api/login', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password }),
      });

      if (res.status === 429) {
        setError('Too many attempts. Try again in a minute.');
        return;
      }
      if (!res.ok) {
        setError('Invalid credentials.');
        return;
      }

      // Clear legacy bearer so the session cookie is the single source
      // of truth from here on.
      authSignal.value = '';
      await loadCurrentUser();
      if (userSignal.value == null) {
        setError('Signed in but failed to load profile. Refresh the page.');
        return;
      }

      // Resume an OAuth /authorize bounce if one sent us here (same-origin only).
      const dest = safeReturnTo(window.location.search, window.location.origin);
      if (dest) {
        window.location.assign(dest);
      }
    } catch {
      setError('Network error. Please retry.');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main class="statnive-login">
      <div class="statnive-login-card">
        <h1 class="statnive-wordmark">
          statnive<em class="statnive-wordmark-live">.live</em>
        </h1>

        {props.demoBanner ? (
          <div class="statnive-login-demo-banner" role="note">
            {props.demoBanner}
          </div>
        ) : null}

        <form onSubmit={onSubmit} noValidate>
          <label>
            <span>Email</span>
            <input
              type="email"
              name="email"
              autoComplete="username"
              required
              value={email}
              onInput={(e) => setEmail((e.target as HTMLInputElement).value)}
            />
          </label>
          <label>
            <span>Password</span>
            <input
              type="password"
              name="password"
              autoComplete="current-password"
              required
              value={password}
              onInput={(e) => setPassword((e.target as HTMLInputElement).value)}
            />
          </label>

          {error ? (
            <p class="statnive-login-error" role="alert">
              {error}
            </p>
          ) : null}

          <button type="submit" disabled={submitting}>
            {submitting ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
      </div>
    </main>
  );
}
