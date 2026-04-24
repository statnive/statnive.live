import { describe, it, expect, beforeEach } from 'vitest';
import { render } from '@testing-library/preact';
import { AppShell } from '../components/AppShell';
import { authCheckedSignal, userSignal } from '../state/auth';
import { sitesSignal, activeSiteSignal } from '../state/site';

// AppShell is a thin layout wrapper around the existing chrome
// components. These tests lock in the structural contract that the
// Playwright e2e specs rely on:
//   - A <main> element wraps children
//   - Nav, DatePicker, FilterPanel, SiteSwitcher all mount
//   - Wordmark with ".live" suffix is present
beforeEach(() => {
  authCheckedSignal.value = true;
  userSignal.value = {
    user_id: 'u-1',
    email: 'test@example.com',
    username: 'tester',
    role: 'admin',
    site_id: 801,
  };
  sitesSignal.value = [];
  activeSiteSignal.value = null;
});

describe('AppShell', () => {
  it('renders topbar + datebar + nav + filter strip + main', () => {
    const { container } = render(
      <AppShell>
        <div data-testid="child">hello</div>
      </AppShell>,
    );

    expect(container.querySelector('.statnive-topbar')).toBeTruthy();
    expect(container.querySelector('.statnive-datebar')).toBeTruthy();
    expect(container.querySelector('.statnive-nav')).toBeTruthy();
    expect(container.querySelector('.statnive-filterpanel')).toBeTruthy();
    expect(container.querySelector('main.statnive-main')).toBeTruthy();
  });

  it('mounts children inside the main element', () => {
    const { getByTestId } = render(
      <AppShell>
        <div data-testid="the-child">payload</div>
      </AppShell>,
    );
    const main = document.querySelector('main.statnive-main');
    const child = getByTestId('the-child');
    expect(main?.contains(child)).toBe(true);
  });

  it('renders the wordmark with .live accent', () => {
    const { container } = render(<AppShell />);
    const wordmark = container.querySelector('.statnive-wordmark');
    expect(wordmark?.textContent).toMatch(/statnive\.live/);
    expect(container.querySelector('.statnive-wordmark-live')).toBeTruthy();
  });

  it('renders Tehran timezone chip', () => {
    const { container } = render(<AppShell />);
    const chip = container.querySelector('.statnive-tz-chip');
    expect(chip?.textContent).toMatch(/Tehran.*UTC.*03:30/);
  });

  it('shows Sign out button only when onLogout is provided', () => {
    const { container: withHandler } = render(
      <AppShell onLogout={() => {}} />,
    );
    expect(withHandler.querySelector('.statnive-logout')).toBeTruthy();

    const { container: noHandler } = render(<AppShell />);
    expect(noHandler.querySelector('.statnive-logout')).toBeNull();
  });
});
