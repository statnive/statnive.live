// Payload-golden test (Phase 7b2): captures the exact JSON shape the
// tracker writes via sendBeacon and pins it to a fixture committed at
// test/fixtures/tracker-payloads.json. The Go integration test
// (test/tracker_correctness_test.go, build tag `integration`) reads the
// same fixture and replays each payload against the full handler →
// pipeline → WAL → consumer → ClickHouse stack, then asserts rollup
// counts match within the 0.05% loss SLO. If this file changes, the
// integration test must still pass — that's the contract.
//
// Regenerate by deleting test/fixtures/tracker-payloads.json and
// re-running `npm test` from tracker/.
import { describe, it, expect, beforeEach } from 'vitest';
import { readFileSync, writeFileSync, existsSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SRC = readFileSync(resolve(__dirname, '../src/tracker.js'), 'utf8');
const FIXTURE_DIR = resolve(__dirname, '../../test/fixtures');
const FIXTURE_PATH = resolve(FIXTURE_DIR, 'tracker-payloads.json');

const ORIG_PUSH = window.history.pushState.bind(window.history);
const ORIG_REPLACE = window.history.replaceState.bind(window.history);
const popstateListeners = new Set();
const origAdd = window.addEventListener.bind(window);
window.addEventListener = function (type, fn, opts) {
  if (type === 'popstate') popstateListeners.add(fn);

  return origAdd(type, fn, opts);
};

function loadTrackerCapture() {
  delete window.statnive;
  window.history.pushState = ORIG_PUSH;
  window.history.replaceState = ORIG_REPLACE;
  for (const fn of popstateListeners) window.removeEventListener('popstate', fn);
  popstateListeners.clear();
  document.head.innerHTML = '';

  const calls = [];
  window.navigator.sendBeacon = (url, blob) => {
    calls.push({ url, blob });

    return true;
  };

  // eslint-disable-next-line no-new-func
  new Function('window', 'document', SRC)(window, document);

  return calls;
}

async function blobText(blob) {
  if (typeof blob.text === 'function') return blob.text();

  return new Promise((res) => {
    const reader = new FileReader();
    reader.onload = () => res(reader.result);
    reader.readAsText(blob);
  });
}

beforeEach(() => {
  Object.defineProperty(window.navigator, 'doNotTrack', { value: '0', configurable: true });
  Object.defineProperty(window.navigator, 'globalPrivacyControl', { value: false, configurable: true });
  Object.defineProperty(window.navigator, 'webdriver', { value: false, configurable: true });
});

describe('tracker payload golden (regression contract)', () => {
  it('captures pageview, custom event, and identified event payload shapes', async () => {
    const fired = [];

    // Pageview on initial load.
    let calls = loadTrackerCapture();
    expect(calls).toHaveLength(1);
    fired.push({ name: 'pageview-initial', body: JSON.parse(await blobText(calls[0].blob)) });

    // Custom event with props + value.
    calls = loadTrackerCapture();
    window.statnive.track('signup', { plan: 'pro', source: 'header' }, 99);
    expect(calls).toHaveLength(2);
    fired.push({ name: 'event-custom', body: JSON.parse(await blobText(calls[1].blob)) });

    // identify() then track() — the user_id field must carry the raw value.
    calls = loadTrackerCapture();
    window.statnive.identify('user_phase7b2_42');
    window.statnive.track('purchase', { sku: 'sku-001' }, 199.5);
    expect(calls).toHaveLength(2);
    fired.push({ name: 'event-identified', body: JSON.parse(await blobText(calls[1].blob)) });

    // Pageview after history.pushState (SPA navigation).
    calls = loadTrackerCapture();
    window.history.pushState({}, '', '/post-navigation');
    expect(calls).toHaveLength(2);
    fired.push({ name: 'pageview-pushstate', body: JSON.parse(await blobText(calls[1].blob)) });

    // Field-shape assertions — these are the contract the Go RawEvent
    // struct also enforces. If the tracker grows a new field, this list
    // must update AND internal/ingest/event.go:RawEvent must mirror it.
    const requiredFields = [
      'hostname', 'pathname', 'title', 'referrer',
      'utm_source', 'utm_medium', 'utm_campaign', 'utm_content', 'utm_term',
      'event_type', 'event_name', 'event_value', 'user_id', 'props',
    ];

    for (const { name, body } of fired) {
      for (const field of requiredFields) {
        expect(body, `${name}: missing field ${field}`).toHaveProperty(field);
      }
    }

    // Persist the golden — first run writes; subsequent runs assert match.
    if (!existsSync(FIXTURE_DIR)) mkdirSync(FIXTURE_DIR, { recursive: true });

    if (!existsSync(FIXTURE_PATH)) {
      writeFileSync(FIXTURE_PATH, JSON.stringify(fired, null, 2) + '\n', 'utf8');

      return;
    }

    const golden = JSON.parse(readFileSync(FIXTURE_PATH, 'utf8'));
    expect(fired.length).toBe(golden.length);

    for (let i = 0; i < fired.length; i++) {
      // hostname/pathname/title vary by jsdom test env — ignore those for
      // the golden comparison and only assert structural fields. The
      // integration test re-injects fresh hostname/pathname per replay.
      const stripVolatile = (b) => {
        const copy = { ...b };
        delete copy.hostname;
        delete copy.pathname;
        delete copy.title;
        delete copy.referrer;

        return copy;
      };
      expect(stripVolatile(fired[i].body), `payload ${fired[i].name} drift`).toEqual(stripVolatile(golden[i].body));
    }
  });
});
