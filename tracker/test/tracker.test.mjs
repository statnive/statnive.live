import { describe, it, expect, beforeEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SRC = readFileSync(resolve(__dirname, '../src/tracker.js'), 'utf8');

// Capture originals once so each loadTracker() can restore them.
const ORIG_PUSH = window.history.pushState.bind(window.history);
const ORIG_REPLACE = window.history.replaceState.bind(window.history);

// popstate + pagehide listeners accumulate across IIFE evaluations; track
// them so we can detach before re-running the IIFE and so tests can
// dispatch the pagehide listener directly.
const popstateListeners = new Set();
const pagehideListeners = new Set();
const origAdd = window.addEventListener.bind(window);
window.addEventListener = function (type, fn, opts) {
  if (type === 'popstate') popstateListeners.add(fn);
  if (type === 'pagehide') pagehideListeners.add(fn);

  return origAdd(type, fn, opts);
};

function loadTracker(opts = {}) {
  delete window.statniveLive;
  if (opts.preExistingStatnive !== undefined) {
    window.statnive = opts.preExistingStatnive;
  } else {
    delete window.statnive;
  }
  delete window._phantom;
  delete window.callPhantom;

  // Restore originals so nested wrappers from prior tests don't stack.
  window.history.pushState = ORIG_PUSH;
  window.history.replaceState = ORIG_REPLACE;

  // Detach popstate + pagehide listeners the IIFE registered earlier.
  for (const fn of popstateListeners) window.removeEventListener('popstate', fn);
  for (const fn of pagehideListeners) window.removeEventListener('pagehide', fn);
  popstateListeners.clear();
  pagehideListeners.clear();

  // Clear any <script> from a previous endpointAttr test.
  document.head.innerHTML = '';

  // Apply per-test navigator overrides.
  if (opts.dnt !== undefined) {
    Object.defineProperty(window.navigator, 'doNotTrack', { value: opts.dnt, configurable: true });
  }
  if (opts.gpc !== undefined) {
    Object.defineProperty(window.navigator, 'globalPrivacyControl', { value: opts.gpc, configurable: true });
  }
  if (opts.webdriver !== undefined) {
    Object.defineProperty(window.navigator, 'webdriver', { value: opts.webdriver, configurable: true });
  }
  if (opts.phantom) window._phantom = {};

  // Three combinations — opts.endpointAttr alone, opts.scriptSrc alone,
  // or both (the "explicit attr wins over derived src" case). The Stage-3
  // honour-gpc opt-in adds a fourth attribute slot that composes with
  // any of the above.
  const honour = opts.honourGpc ? ' data-statnive-honour-gpc="1"' : '';
  if (opts.endpointAttr && opts.scriptSrc) {
    document.head.innerHTML =
      `<script src="${opts.scriptSrc}" data-statnive-endpoint="${opts.endpointAttr}"${honour}></script>`;
  } else if (opts.endpointAttr) {
    document.head.innerHTML = `<script data-statnive-endpoint="${opts.endpointAttr}"${honour}></script>`;
  } else if (opts.scriptSrc) {
    // Simulates the cross-origin marketing-site case: <script src=
    // "https://statnive.live/tracker.js"> with no data-statnive-endpoint.
    // Bug #18 — tracker should derive /api/event from the src origin
    // instead of falling back to a relative /api/event 404 sink.
    document.head.innerHTML = `<script src="${opts.scriptSrc}" async defer${honour}></script>`;
  } else if (opts.honourGpc) {
    document.head.innerHTML = `<script${honour}></script>`;
  }

  // Mock sendBeacon so we can inspect the payload.
  const beaconCalls = [];
  window.navigator.sendBeacon = (url, blob) => {
    beaconCalls.push({ url, body: blob });

    return true;
  };

  // Evaluate the tracker source in this window.
  // eslint-disable-next-line no-new-func
  new Function('window', 'document', SRC)(window, document);

  return beaconCalls;
}

async function blobText(blob) {
  if (typeof blob.text === 'function') return blob.text();

  return new Promise((res) => {
    const reader = new FileReader();
    reader.onload = () => res(reader.result);
    reader.readAsText(blob);
  });
}

// Reset navigator privacy flags to neutral defaults before every spec.
// Tests that need a non-default value pass it via loadTracker(opts) below.
// Also wipe consent / segment cookies + sessionStorage so each spec starts
// in the 'idle' consent state.
beforeEach(() => {
  Object.defineProperty(window.navigator, 'doNotTrack', { value: '0', configurable: true });
  Object.defineProperty(window.navigator, 'globalPrivacyControl', { value: false, configurable: true });
  Object.defineProperty(window.navigator, 'webdriver', { value: false, configurable: true });
  // Clear segment + consent storage between tests.
  for (const k of ['sn_consent', 'sn_user_props']) {
    document.cookie = k + '=; Max-Age=0; Path=/';
  }
  try { window.sessionStorage.removeItem('sn_sess_props'); } catch (e) {}
});

describe('anti-automation short-circuit', () => {
  it('fires nothing when navigator.webdriver=true', () => {
    const calls = loadTracker({ webdriver: true });
    expect(calls).toHaveLength(0);
    expect(typeof window.statniveLive.track).toBe('function');
    expect(typeof window.statniveLive.identify).toBe('function');
  });

  it('fires nothing when window._phantom is set', () => {
    const calls = loadTracker({ phantom: true });
    expect(calls).toHaveLength(0);
  });

  it('subsequent track() calls are no-ops after webdriver short-circuit', () => {
    const calls = loadTracker({ webdriver: true });
    window.statniveLive.track('would-not-fire');
    window.statniveLive.identify('user-x');
    expect(calls).toHaveLength(0);
  });
});

// Regression for the wp-slimstat.com 2026-05-04 zero-beacon bug: the
// WP plugin (a separate same-brand product) sets `window.statnive` as a
// queue-stub function before our SaaS tracker arrives. A pre-rename
// guard `if (w.statnive) return;` made the SaaS tracker bail silently
// — tracker.js HTTP 200 in DevTools, zero /api/event POSTs ever fired.
// Renaming the public surface to `window.statniveLive` removes the
// collision; this test pins the new behavior.
describe('wp-plugin global collision', () => {
  it('fires a pageview even when window.statnive is already a foreign queue stub', () => {
    const stub = function () { (stub.q = stub.q || []).push(arguments); };
    const calls = loadTracker({ preExistingStatnive: stub });
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe('/api/event');
    expect(typeof window.statniveLive.track).toBe('function');
    // The foreign stub on window.statnive must remain untouched so the
    // co-existing WP plugin keeps working.
    expect(window.statnive).toBe(stub);
  });
});

// DNT / Sec-GPC are NOT consulted client-side anymore — browsers attach
// the headers automatically and the server honors them per
// consent.respect_dnt / consent.respect_gpc (default off). The tracker
// must still POST so the server can apply per-deployment policy, count
// the visit, and (when respect=on) suppress identity per Privacy Rule 9.
describe('DNT / Sec-GPC fire POST (server decides)', () => {
  it('fires a pageview when DNT=1', () => {
    const calls = loadTracker({ dnt: '1' });
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe('/api/event');
  });

  it('fires a pageview when Sec-GPC=true', () => {
    const calls = loadTracker({ gpc: true });
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe('/api/event');
  });

  it('fires a pageview when both DNT=1 and Sec-GPC=true', () => {
    const calls = loadTracker({ dnt: '1', gpc: true });
    expect(calls).toHaveLength(1);
  });
});

describe('happy path', () => {
  it('fires a pageview on initial load with hostname/pathname', async () => {
    const calls = loadTracker();
    expect(calls).toHaveLength(1);
    const body = JSON.parse(await blobText(calls[0].body));
    expect(body.event_type).toBe('pageview');
    expect(body.event_name).toBe('pageview');
    expect(body.hostname).toBe(window.location.hostname);
    expect(body.pathname).toBe(window.location.pathname);
    expect(body.user_id).toBe('');
  });

  it('track() emits a custom event with hit_props + value', async () => {
    const calls = loadTracker();
    window.statniveLive.track('signup', { plan: 'pro' }, 99);
    expect(calls).toHaveLength(2);
    const body = JSON.parse(await blobText(calls[1].body));
    expect(body.event_type).toBe('custom');
    expect(body.event_name).toBe('signup');
    expect(body.event_value).toBe(99);
    // Phase 1 rename: `props` → `hit_props` envelope key. Server's
    // RawEvent.Props alias-merges hit_props for one release for backward
    // compatibility on customers running an older tracker.
    expect(body.hit_props).toEqual({ plan: 'pro' });
    expect(body.session_props).toEqual({});
    expect(body.user_props).toEqual({});
  });

  it('history.pushState fires a pageview', async () => {
    const calls = loadTracker();
    window.history.pushState({}, '', '/new-route');
    expect(calls).toHaveLength(2);
    const body = JSON.parse(await blobText(calls[1].body));
    expect(body.event_type).toBe('pageview');
    expect(body.pathname).toBe('/new-route');
  });

  it('history.replaceState fires a pageview', async () => {
    const calls = loadTracker();
    window.history.replaceState({}, '', '/replaced');
    expect(calls).toHaveLength(2);
    const body = JSON.parse(await blobText(calls[1].body));
    expect(body.pathname).toBe('/replaced');
  });

  it('popstate fires a pageview', () => {
    const calls = loadTracker();
    window.dispatchEvent(new PopStateEvent('popstate'));
    expect(calls).toHaveLength(2);
  });

  it('uses the data-statnive-endpoint attribute when present', () => {
    const calls = loadTracker({ endpointAttr: '/custom/api/event' });
    expect(calls[0].url).toBe('/custom/api/event');
  });

  it('defaults to /api/event without the attribute', () => {
    const calls = loadTracker();
    expect(calls[0].url).toBe('/api/event');
  });

  // Bug #18 (Milestone 1 cutover): on cross-origin marketing sites the
  // tracker is loaded via <script src="https://statnive.live/tracker.js">
  // with no data-statnive-endpoint. The relative /api/event default 404'd
  // against the marketing-site origin. Fallback derives endpoint from
  // script.src so the canonical embed pattern works without operator
  // ceremony. Closes LEARN.md Lesson 17 preventive measure.
  it('derives endpoint from script.src when no data-attribute (cross-origin)', () => {
    const calls = loadTracker({ scriptSrc: 'https://statnive.live/tracker.js' });
    expect(calls[0].url).toBe('https://statnive.live/api/event');
  });

  it('derives endpoint from script.src when src has querystring', () => {
    const calls = loadTracker({ scriptSrc: 'https://statnive.live/tracker.js?v=1' });
    expect(calls[0].url).toBe('https://statnive.live/api/event');
  });

  it('explicit data-statnive-endpoint wins over script.src', () => {
    const calls = loadTracker({
      scriptSrc: 'https://statnive.live/tracker.js',
      endpointAttr: '/override/api/event',
    });
    expect(calls[0].url).toBe('/override/api/event');
  });
});

describe('pagehide backstop', () => {
  function firePagehide() {
    for (const fn of pagehideListeners) fn();
  }

  it('registers a pagehide handler for the unload backstop', () => {
    loadTracker();
    expect(pagehideListeners.size).toBeGreaterThan(0);
  });

  it('does not double-fire when the inline pageview already ran', () => {
    const calls = loadTracker();
    expect(calls).toHaveLength(1); // initial pageview
    firePagehide();
    expect(calls).toHaveLength(1); // pagehide is a no-op
  });

  it('SPA navigation + pagehide does not double-count', () => {
    const calls = loadTracker();
    window.history.pushState({}, '', '/route-a');
    expect(calls).toHaveLength(2);
    firePagehide();
    expect(calls).toHaveLength(2);
  });

  it('replaceState + pagehide does not double-count', () => {
    const calls = loadTracker();
    window.history.replaceState({}, '', '/replaced');
    expect(calls).toHaveLength(2);
    firePagehide();
    expect(calls).toHaveLength(2);
  });

  it('custom track() bypasses the sentinel and pagehide stays a no-op', () => {
    const calls = loadTracker();
    window.statniveLive.track('signup', { plan: 'pro' }, 99);
    expect(calls).toHaveLength(2);
    firePagehide();
    expect(calls).toHaveLength(2);
  });

  it('SPA route reset lets a fresh pageview fire on the next nav', () => {
    const calls = loadTracker();
    window.history.pushState({}, '', '/a');
    window.history.pushState({}, '', '/b');
    expect(calls).toHaveLength(3); // initial + /a + /b
    firePagehide();
    expect(calls).toHaveLength(3);
  });
});

describe('identify (user_id)', () => {
  it('passes raw uid through to subsequent payloads', async () => {
    const calls = loadTracker();
    window.statniveLive.identify('user_a83f');
    window.statniveLive.track('purchase', {}, 100);
    expect(calls).toHaveLength(2);
    const body = JSON.parse(await blobText(calls[1].body));
    expect(body.user_id).toBe('user_a83f');
  });

  it('identify(null) clears the previous uid', async () => {
    const calls = loadTracker();
    window.statniveLive.identify('user_a83f');
    window.statniveLive.track('a', {}, 0);
    window.statniveLive.identify(null);
    window.statniveLive.track('b', {}, 0);
    const bodyA = JSON.parse(await blobText(calls[1].body));
    const bodyB = JSON.parse(await blobText(calls[2].body));
    expect(bodyA.user_id).toBe('user_a83f');
    expect(bodyB.user_id).toBe('');
  });

  it('uid is the raw value (not a hash) — server hashes it', async () => {
    const calls = loadTracker();
    window.statniveLive.identify('plain_uid_42');
    window.statniveLive.track('test', {}, 0);
    const body = JSON.parse(await blobText(calls[1].body));
    // The tracker MUST NOT hash client-side. Server hashes via
    // identity.HexUserIDHash with the master_secret, which the tracker
    // doesn't have — so any client-side hashing would be insecure too.
    expect(body.user_id).toBe('plain_uid_42');
    expect(body.user_id).not.toMatch(/^[a-f0-9]{64}$/);
  });
});

// Stage 3 — opt-in GPC probe + consent helpers.
describe('honour-GPC opt-in', () => {
  it('skips ingest when honour-gpc=1 AND globalPrivacyControl=true', () => {
    const calls = loadTracker({ honourGpc: true, gpc: true });
    expect(calls).toHaveLength(0);
    // Stub surface still present so the operator's banner JS doesn't TypeError.
    expect(typeof window.statniveLive.acceptConsent).toBe('function');
  });

  it('fires normally when honour-gpc=1 AND globalPrivacyControl=false', () => {
    const calls = loadTracker({ honourGpc: true, gpc: false });
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe('/api/event');
  });

  it('fires normally when honour-gpc absent AND globalPrivacyControl=true (legacy posture)', () => {
    const calls = loadTracker({ gpc: true });
    expect(calls).toHaveLength(1);
  });
});

describe('consent helpers', () => {
  it('acceptConsent POSTs to /api/privacy/consent with action=give + CSRF header', async () => {
    const fetchCalls = [];
    const origFetch = window.fetch;
    window.fetch = (url, init) => { fetchCalls.push({ url, init }); return Promise.resolve({ status: 204 }); };

    try {
      loadTracker();
      await window.statniveLive.acceptConsent('csrf-abc');

      expect(fetchCalls).toHaveLength(1);
      expect(fetchCalls[0].url).toBe('/api/privacy/consent');
      expect(fetchCalls[0].init.method).toBe('POST');
      // Stage-4 PR 4-C: cross-origin SaaS support needs `credentials: 'include'`
      // so the __Host-statnive_csrf cookie traverses tracker.<operator> →
      // app.statnive.live POSTs. Same-origin sites still attach cookies under
      // the same flag.
      expect(fetchCalls[0].init.credentials).toBe('include');
      expect(fetchCalls[0].init.headers['X-CSRF-Token']).toBe('csrf-abc');
      const body = JSON.parse(fetchCalls[0].init.body);
      expect(body.action).toBe('give');
    } finally {
      window.fetch = origFetch;
    }
  });

  it('withdrawConsent POSTs action=withdraw', async () => {
    const fetchCalls = [];
    const origFetch = window.fetch;
    window.fetch = (url, init) => { fetchCalls.push({ url, init }); return Promise.resolve({ status: 204 }); };

    try {
      loadTracker();
      await window.statniveLive.withdrawConsent('csrf-xyz');
      expect(JSON.parse(fetchCalls[0].init.body).action).toBe('withdraw');
    } finally {
      window.fetch = origFetch;
    }
  });

  it('hits the same origin as /api/event when endpoint is cross-origin', async () => {
    const fetchCalls = [];
    const origFetch = window.fetch;
    window.fetch = (url, init) => { fetchCalls.push({ url, init }); return Promise.resolve({ status: 204 }); };

    try {
      loadTracker({ scriptSrc: 'https://app.statnive.live/tracker.js' });
      await window.statniveLive.acceptConsent('t');

      expect(fetchCalls[0].url).toBe('https://app.statnive.live/api/privacy/consent');
    } finally {
      window.fetch = origFetch;
    }
  });
});

// ─────────────────────────────────────────────────────────────────────────
// Phase 1 — Segments consent gate (Plan § 1 + § 11 deployment-mode matrix)
// ─────────────────────────────────────────────────────────────────────────
//
// Three-state local machine: 'idle' | 'resolved' | 'withdrawn'. Reads from
// the sn_consent cookie (absence == 'idle'). setSession / identify-with-
// userProps are silent no-ops unless state === 'resolved'.

describe('getConsent() state machine', () => {
  it("returns 'idle' on a fresh page with no cookie", () => {
    loadTracker();
    expect(window.statniveLive.getConsent()).toBe('idle');
  });

  it("returns 'resolved' when sn_consent=resolved cookie is present", () => {
    document.cookie = 'sn_consent=resolved; Path=/';
    loadTracker();
    expect(window.statniveLive.getConsent()).toBe('resolved');
  });

  it("returns 'withdrawn' when sn_consent=withdrawn cookie is present", () => {
    document.cookie = 'sn_consent=withdrawn; Path=/';
    loadTracker();
    expect(window.statniveLive.getConsent()).toBe('withdrawn');
  });

  it("returns 'idle' on the anti-automation noop API (webdriver short-circuit)", () => {
    loadTracker({ webdriver: true });
    expect(window.statniveLive.getConsent()).toBe('idle');
  });

  it("returns 'withdrawn' on the honour-GPC short-circuit noop API", () => {
    loadTracker({ honourGpc: true, gpc: true });
    expect(window.statniveLive.getConsent()).toBe('withdrawn');
  });
});

describe('setSession (session-scope props)', () => {
  it('is a silent no-op when getConsent() === idle', async () => {
    const calls = loadTracker();
    window.statniveLive.setSession({ ab_variant: 'B' });
    // Should not throw, should not persist.
    expect(window.sessionStorage.getItem('sn_sess_props')).toBeNull();
    window.statniveLive.track('event', {}, 0);
    const body = JSON.parse(await blobText(calls[1].body));
    expect(body.session_props).toEqual({});
  });

  it('persists to sessionStorage when getConsent() === resolved', async () => {
    document.cookie = 'sn_consent=resolved; Path=/';
    const calls = loadTracker();
    window.statniveLive.setSession({ ab_variant: 'B', src: 'fb_ad' });
    expect(JSON.parse(window.sessionStorage.getItem('sn_sess_props'))).toEqual({
      ab_variant: 'B',
      src: 'fb_ad',
    });
    window.statniveLive.track('event', {}, 0);
    const body = JSON.parse(await blobText(calls[1].body));
    expect(body.session_props).toEqual({ ab_variant: 'B', src: 'fb_ad' });
  });

  it('is a no-op after withdrawConsent (state === withdrawn)', () => {
    document.cookie = 'sn_consent=withdrawn; Path=/';
    loadTracker();
    window.statniveLive.setSession({ ab_variant: 'B' });
    expect(window.sessionStorage.getItem('sn_sess_props')).toBeNull();
  });
});

describe('identify (user-scope props)', () => {
  it('userProps arg is a silent no-op when getConsent() === idle', async () => {
    const calls = loadTracker();
    window.statniveLive.identify('user-42', { plan: 'pro' });
    expect(document.cookie).not.toContain('sn_user_props');
    window.statniveLive.track('event', {}, 0);
    const body = JSON.parse(await blobText(calls[1].body));
    expect(body.user_id).toBe('user-42');
    expect(body.user_props).toEqual({});
  });

  it('persists userProps to sn_user_props cookie when consent is resolved', async () => {
    document.cookie = 'sn_consent=resolved; Path=/';
    const calls = loadTracker();
    window.statniveLive.identify('user-42', { plan: 'pro', signup_year: '2026' });
    expect(document.cookie).toContain('sn_user_props');
    window.statniveLive.track('event', {}, 0);
    const body = JSON.parse(await blobText(calls[1].body));
    expect(body.user_props).toEqual({ plan: 'pro', signup_year: '2026' });
  });

  it('uid-only identify (no userProps arg) still works', async () => {
    const calls = loadTracker();
    window.statniveLive.identify('user-42');
    window.statniveLive.track('event', {}, 0);
    const body = JSON.parse(await blobText(calls[1].body));
    expect(body.user_id).toBe('user-42');
  });
});

describe('consent state transitions', () => {
  it('acceptConsent success sets sn_consent=resolved cookie', async () => {
    const origFetch = window.fetch;
    window.fetch = () => Promise.resolve({ ok: true, status: 204 });
    try {
      loadTracker();
      expect(window.statniveLive.getConsent()).toBe('idle');
      await window.statniveLive.acceptConsent('csrf-abc');
      expect(window.statniveLive.getConsent()).toBe('resolved');
    } finally {
      window.fetch = origFetch;
    }
  });

  it('acceptConsent does NOT flip cookie when the POST fails', async () => {
    const origFetch = window.fetch;
    window.fetch = () => Promise.resolve({ ok: false, status: 500 });
    try {
      loadTracker();
      await window.statniveLive.acceptConsent('csrf-abc');
      expect(window.statniveLive.getConsent()).toBe('idle');
    } finally {
      window.fetch = origFetch;
    }
  });

  it('withdrawConsent clears sn_user_props cookie + sn_sess_props storage + flips state', async () => {
    // Seed resolved state with both stores populated.
    document.cookie = 'sn_consent=resolved; Path=/';
    document.cookie = 'sn_user_props=' + encodeURIComponent(JSON.stringify({ plan: 'pro' })) + '; Path=/';
    window.sessionStorage.setItem('sn_sess_props', JSON.stringify({ ab_variant: 'B' }));
    const origFetch = window.fetch;
    window.fetch = () => Promise.resolve({ ok: true, status: 204 });
    try {
      loadTracker();
      await window.statniveLive.withdrawConsent('csrf-xyz');
      expect(window.statniveLive.getConsent()).toBe('withdrawn');
      expect(document.cookie).not.toContain('sn_user_props=');
      expect(window.sessionStorage.getItem('sn_sess_props')).toBeNull();
    } finally {
      window.fetch = origFetch;
    }
  });
});

describe('wire envelope consent gating', () => {
  it('hit_props always travels (carries no persistent identifier)', async () => {
    const calls = loadTracker();
    window.statniveLive.track('cta', { button: 'hero' }, 0);
    const body = JSON.parse(await blobText(calls[1].body));
    expect(body.hit_props).toEqual({ button: 'hero' });
  });

  it('session_props + user_props are empty in idle state, even if storage is populated', async () => {
    // Force storage population WITHOUT a resolved consent cookie. This
    // simulates a partial-state edge case (e.g., operator wiped sn_consent
    // but left the other cookies). Behavior: ignore them.
    document.cookie = 'sn_user_props=' + encodeURIComponent(JSON.stringify({ plan: 'pro' })) + '; Path=/';
    window.sessionStorage.setItem('sn_sess_props', JSON.stringify({ ab_variant: 'B' }));
    const calls = loadTracker();
    const body = JSON.parse(await blobText(calls[0].body));
    expect(body.user_props).toEqual({});
    expect(body.session_props).toEqual({});
  });

  it('session_props + user_props populate in resolved state', async () => {
    document.cookie = 'sn_consent=resolved; Path=/';
    document.cookie = 'sn_user_props=' + encodeURIComponent(JSON.stringify({ plan: 'pro' })) + '; Path=/';
    window.sessionStorage.setItem('sn_sess_props', JSON.stringify({ ab_variant: 'B' }));
    const calls = loadTracker();
    const body = JSON.parse(await blobText(calls[0].body));
    expect(body.user_props).toEqual({ plan: 'pro' });
    expect(body.session_props).toEqual({ ab_variant: 'B' });
  });
});
