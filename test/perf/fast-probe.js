// k6 Fast-probe — Phase 7e capacity probe against the production Netcup VPS.
//
// Goal: answer "max sustained EPS the production VPS holds before SLOs break"
// in ONE 75-minute ramp. Skips schema migration 007, chaos matrix, observability
// VPS — those land in the Full Phase 7e gate.
//
// Hostname `load-test.example.com` is intentionally UNSEEDED in production, so
// every event lands in `dropped_total{reason="hostname_unknown"}`. This gives
// us:
//   - real TCP/TLS/middleware/parse/rate-limit cost measurement
//   - zero pollution of any customer or operator site_id dashboard
//   - clean reconciliation: received_total delta == dropped_total{hostname_unknown} delta
//
// Run:
//   STATNIVE_URL=https://app.statnive.live make fast-probe
//
// SLO budget per doc 29 §4 P1 + the operator's stop-conditions:
//   - http_req_failed rate < 0.01 (1%)
//   - http_req_duration p99 < 2000ms (probe ceiling, plan ceiling is 500ms)
//
// Stop conditions (manual, watching `/metrics` in parallel SSH):
//   - dropped_total{reason} grows in any reason OTHER than hostname_unknown
//   - wal_fsync_p99_ms crosses 100ms
//   - wal_fill_ratio crosses 0.80
//   - any 5xx from the binary
//
// Plan: ~/.claude/plans/phase-7e-load-gate-scaffolding-wise-puppy.md

import http from 'k6/http';
import { check } from 'k6';
import { SharedArray } from 'k6/data';

const visitors = new SharedArray('visitors', () =>
  Array.from({ length: 1500 }, (_, i) => `v-${i.toString(16).padStart(8, '0')}`),
);

const persianPaths = ['/خانه', '/فیلم', '/سریال', '/تماس', '/درباره-ما', '/ورود', '/ثبت-نام'];

// Real-browser UAs (≥16 chars per LEARN.md Lesson 15 fast-reject floor).
const iranianUAs = [
  'Mozilla/5.0 (Linux; Android 13; SM-A536B) AppleWebKit/537.36 Chrome/120 Mobile',
  'Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1 Version/17.2 Mobile/15E148 Safari/604.1',
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_2) AppleWebKit/605.1 Version/17.2 Safari/605.1',
  'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120',
];

const targetURL = __ENV.STATNIVE_URL || 'http://127.0.0.1:8080';

export const options = {
  scenarios: {
    fast_probe: {
      executor: 'ramping-arrival-rate',
      startRate: 50,
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 500,
      stages: [
        { target: 100, duration: '5m' },   // 0→100 warm-up
        { target: 100, duration: '15m' },  // 100 sustained — baseline observation
        { target: 300, duration: '5m' },   // 100→300 ramp
        { target: 300, duration: '30m' },  // 300 sustained — headline measurement
        { target: 500, duration: '5m' },   // 300→500 breakpoint sweep
        { target: 0, duration: '15m' },    // ramp down + cool-off observation
      ],
      gracefulStop: '30s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(99)<2000'],
  },
};

export default function () {
  const v = visitors[Math.floor(Math.random() * visitors.length)];
  const ua = iranianUAs[Math.floor(Math.random() * iranianUAs.length)];
  const path = persianPaths[Math.floor(Math.random() * persianPaths.length)];

  const body = JSON.stringify({
    hostname: 'load-test.example.com',
    pathname: path,
    event_type: 'pageview',
    event_name: 'pageview',
  });

  const res = http.post(`${targetURL}/api/event`, body, {
    headers: {
      'User-Agent': ua,
      Cookie: `_statnive=${v}`,
      // 192.0.2.0/24 documentation range — never routable.
      'X-Forwarded-For': `192.0.2.${Math.floor(Math.random() * 254) + 1}`,
      'Content-Type': 'text/plain',
    },
    timeout: '10s',
  });

  check(res, { 'fast-rejected (204) or accepted (202)': (r) => r.status === 204 || r.status === 202 });
}

export function handleSummary(data) {
  const summary = {
    iterations: data.metrics.iterations.values.count,
    iteration_rate_per_sec: data.metrics.iterations.values.rate,
    http_req_duration_p50_ms: data.metrics.http_req_duration.values['p(50)'],
    http_req_duration_p95_ms: data.metrics.http_req_duration.values['p(95)'],
    http_req_duration_p99_ms: data.metrics.http_req_duration.values['p(99)'],
    http_req_failed_rate: data.metrics.http_req_failed.values.rate,
  };

  return {
    stdout:
      JSON.stringify(summary, null, 2) +
      '\n\nReconcile against the binary:\n' +
      `  ssh ops@94.16.108.78 'curl -k -fsS -m 10 -H "Authorization: Bearer $TOKEN" https://127.0.0.1/metrics' | grep -E "(received_total|dropped_total|wal_fill|wal_fsync)"\n` +
      `  Expected: dropped_total{reason="hostname_unknown"} delta == k6 iterations (${summary.iterations})\n` +
      `  Loss budget: <0.05% of iterations\n`,
  };
}
