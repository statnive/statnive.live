import { useState } from 'preact/hooks';
import { loadCurrentUser, userSignal, authSignal } from '../state/auth';
import './Login.css';

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
