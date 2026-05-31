// k6-crosscheck.js — CI sanity cross-check for the Locust primary.
//
// Same /api/event payload + oracle tuple as the Locust harness; lower
// EPS by design (10 VUs × 60s) so it fits in a 2-minute CI step. If
// k6 reports a regression that Locust doesn't, the binary has a CI-
// reachable issue that prod doesn't hit; if Locust reports one that k6
// doesn't, the regression is load-shape sensitive (good information).
//
// Run via:
//
//   make load-gate-crosscheck                # k6 only
//   k6 run test/perf/gate/k6-crosscheck.js   # direct
//
// Env vars: STATNIVE_URL (default http://127.0.0.1:8080)
//           SITE_ID       (default 1)
//           HOSTNAME      (default load-test.example.com)
//           TEST_RUN_ID   (default = fresh UUID)
//
// SLO budget: same as Locust phase P1 (p99 < 2000ms, err < 0.5%).
// k6's thresholds: fail the run if either breaches.

import http from 'k6/http';
import { check } from 'k6';
import { uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

const BASE = __ENV.STATNIVE_URL || 'http://127.0.0.1:8080';
const SITE_ID = parseInt(__ENV.SITE_ID || '1', 10);
const HOSTNAME = __ENV.HOSTNAME || 'load-test.example.com';
const TEST_RUN_ID = __ENV.TEST_RUN_ID || uuidv4();

export const options = {
  scenarios: {
    crosscheck: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '10s', target: 10 },
        { duration: '40s', target: 10 },
        { duration: '10s', target: 0 },
      ],
      gracefulStop: '5s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.005'],            // 0.5%
    http_req_duration: ['p(99)<2000'],          // 2000 ms
  },
};

const UA =
  'Mozilla/5.0 (Linux; Android 14; SM-S921B) AppleWebKit/537.36 ' +
  '(KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36';
const PATHS = ['/', '/blog', '/pricing', '/checkout'];

let seq = 0;

export function setup() {
  console.log(`\n  TEST_RUN_ID=${TEST_RUN_ID}`);
  console.log(`  (pass to: make oracle-scan TEST_RUN_ID=${TEST_RUN_ID})\n`);
  return { runId: TEST_RUN_ID };
}

export default function (data) {
  seq += 1;
  const path = PATHS[seq % PATHS.length];
  const nodeId = (__VU * 65537) % 65536;

  const payload = JSON.stringify({
    hostname: HOSTNAME,
    pathname: path,
    title: path === '/' ? 'Home' : path.slice(1),
    referrer: 'https://www.google.com/',
    utm_source: 'google',
    utm_medium: 'organic',
    viewport_width: 390,
    event_type: 'pageview',
    event_name: 'pageview',
    user_id: `load-gate-uid-${seq % 256}`,
    test_run_id: data.runId,
    test_generator_seq: seq,
    generator_node_id: nodeId,
    send_ts_ms: Date.now(),
  });

  const res = http.post(`${BASE}/api/event`, payload, {
    headers: {
      'Content-Type': 'text/plain',
      'User-Agent': UA,
      Host: HOSTNAME,
    },
  });

  check(res, {
    'status is 2xx': (r) => r.status >= 200 && r.status < 300,
  });
}

export function teardown(data) {
  console.log(`\n--- k6 cross-check done ---`);
  console.log(`test_run_id: ${data.runId}\n`);
}
