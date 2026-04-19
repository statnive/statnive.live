// k6 load script — 7K EPS sustained against /api/event for 5 minutes.
//
// Filimo's peak-hour traffic at 10–20M DAU lands around 7K EPS per
// PLAN.md doc 16. This script proves the binary holds at that rate
// with the analytics-invariant budget:
//   - http_req_failed rate < 0.001  (< 0.1% errors)
//   - http_req_duration p95 < 50ms
//   - http_req_duration p99 < 200ms
//
// Post-run, the operator runs:
//   clickhouse-client -q "SELECT count() FROM statnive.events_raw \
//                         WHERE hostname='load-test.example.com'"
// and verifies the count is within 0.05% of k6's iterations counter
// (the CLAUDE.md server-side event-loss budget).
//
// Pre-flight: seed the site row.
//   docker exec statnive-clickhouse-dev clickhouse-client -q \
//     "INSERT INTO statnive.sites (site_id, hostname, slug, enabled) \
//      VALUES (999, 'load-test.example.com', 'load-test', 1)"

import http from 'k6/http';
import { check } from 'k6';
import { SharedArray } from 'k6/data';

// 1500-visitor pool — each VU picks one cookie at random per request.
// Below the burst-guard cap (500/min/visitor) at peak load.
const visitors = new SharedArray('visitors', () => {
  return Array.from({ length: 1500 }, (_, i) =>
    `v-${i.toString(16).padStart(8, '0')}`,
  );
});

const persianPaths = [
  '/خانه',
  '/فیلم',
  '/سریال',
  '/تماس',
  '/درباره-ما',
  '/ورود',
  '/ثبت-نام',
];

const iranianUAs = [
  'Mozilla/5.0 (Linux; Android 13; SM-A536B) AppleWebKit/537.36 Chrome/120 Mobile',
  'Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1 Version/17.2 Mobile/15E148 Safari/604.1',
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_2) AppleWebKit/605.1 Version/17.2 Safari/605.1',
  'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120',
];

const targetURL = __ENV.STATNIVE_URL || 'http://127.0.0.1:8080';

export const options = {
  scenarios: {
    ramp_to_peak: {
      executor: 'ramping-arrival-rate',
      startRate: 100,
      timeUnit: '1s',
      preAllocatedVUs: 200,
      maxVUs: 1000,
      stages: [
        { target: 7000, duration: '1m' },  // ramp to 7K EPS
        { target: 7000, duration: '3m' },  // hold at peak
        { target: 0, duration: '30s' },    // ramp down
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.001'],
    http_req_duration: ['p(95)<50', 'p(99)<200'],
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
      // 192.0.2.0/24 is the IETF-reserved documentation range — never
      // routable, so no real PII enters anything we touch.
      'X-Forwarded-For': `192.0.2.${Math.floor(Math.random() * 254) + 1}`,
      'Content-Type': 'text/plain',
    },
  });

  check(res, { '2xx': (r) => r.status >= 200 && r.status < 300 });
}

export function handleSummary(data) {
  const summary = {
    iterations: data.metrics.iterations.values.count,
    http_req_duration_p95_ms: data.metrics.http_req_duration.values['p(95)'],
    http_req_duration_p99_ms: data.metrics.http_req_duration.values['p(99)'],
    http_req_failed_rate: data.metrics.http_req_failed.values.rate,
    iteration_rate_per_sec: data.metrics.iterations.values.rate,
  };

  return {
    stdout: JSON.stringify(summary, null, 2) + '\n\nVerify in ClickHouse:\n  ' +
      `clickhouse-client -q "SELECT count() FROM statnive.events_raw WHERE hostname=\\'load-test.example.com\\'"\n` +
      `Expected: ${summary.iterations} ± ${Math.ceil(summary.iterations * 0.0005)} (0.05% loss budget)\n`,
  };
}
