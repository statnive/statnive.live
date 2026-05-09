// Playwright globalSetup — boots the real cmd/statnive-live binary
// against docker-compose ClickHouse, seeds two sites + a deterministic
// event fixture, then hands control back to the test runner. Teardown
// kills the binary + cleans CH rows.
//
// Canonical reference for the spawn + seed shape is test/smoke/harness.sh
// (Phase 5a-smoke harness). Any STATNIVE_* env-var change on the harness
// needs a mirror here so the two entry-points stay in sync. This module
// is a TS port not a bash invocation because harness.sh exits after its
// probe matrix (it's not a long-running server), so the spawn + wait +
// seed logic has to live somewhere testable from Playwright traces.

import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdtempSync, writeFileSync, chmodSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import { chExec } from './fixtures/chOracle';

// Defaults deliberately distinct from smoke harness (18199 / 997 /
// smoke-tok-abc) so a locally-running smoke doesn't contaminate e2e
// rows and vice-versa.
const PORT = Number(process.env.STATNIVE_E2E_PORT ?? 18299);
const BEARER = process.env.STATNIVE_E2E_TOKEN ?? 'e2e-tok-xyz';
const SITE_A = Number(process.env.STATNIVE_E2E_SITE_A ?? 801);
const SITE_B = Number(process.env.STATNIVE_E2E_SITE_B ?? 802);
const HOST_A = process.env.STATNIVE_E2E_HOST_A ?? 'e2e-a.example.com';
const HOST_B = process.env.STATNIVE_E2E_HOST_B ?? 'e2e-b.example.com';
const CH_CONTAINER = process.env.STATNIVE_E2E_CH_CONTAINER ?? 'statnive-clickhouse-dev';
const CH_ADDR = process.env.STATNIVE_E2E_CH_ADDR ?? '127.0.0.1:19000';

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, '../..');
const BIN_PATH = join(REPO_ROOT, 'bin/statnive-live');

function cleanSite(siteID: number, hostname: string): void {
  const tables = [
    'statnive.events_raw',
    'statnive.hourly_visitors',
    'statnive.daily_pages',
    'statnive.daily_sources',
  ];
  for (const tbl of tables) {
    try {
      chExec(`ALTER TABLE ${tbl} DELETE WHERE site_id = ${siteID} SETTINGS mutations_sync = 2`);
    } catch {
      // First-run: tables may not exist yet; harmless.
    }
  }
  try {
    chExec(
      `ALTER TABLE statnive.sites DELETE WHERE site_id = ${siteID} OR hostname = '${hostname}' SETTINGS mutations_sync = 2`,
    );
  } catch {
    // First-run: sites row may not exist.
  }
}

function seedSite(siteID: number, hostname: string, tz: string): void {
  chExec(
    `INSERT INTO statnive.sites (site_id, hostname, slug, enabled, tz) VALUES (${siteID}, '${hostname}', 'e2e-${siteID}', 1, '${tz}')`,
  );
}

interface SeedRow {
  siteID: number;
  minutesAgo: number;
  pathname: string;
  referrerName: string;
  channel: string;
  utmCampaign: string;
  deviceType: string;
  country: string;
  visitorHex: string; // 32-char hex, represents FixedString(16)
  isGoal: number;
  revenue: number;
}

function seedEvents(rows: SeedRow[]): void {
  // INSERT via VALUES; matches storagetest.WriteEvents' event shape with
  // device_type / browser / country_code set so filter tests can assert
  // on enriched dimensions. user_id_hash + cookie_id stay empty.
  if (rows.length === 0) return;

  const values = rows.map((r) => {
    const ts = new Date(Date.now() - r.minutesAgo * 60_000).toISOString().replace('T', ' ').slice(0, 19);
    return `(
      ${r.siteID}, '${ts}', '', '',
      unhex('${r.visitorHex}'),
      '', '${r.pathname}', '', '', '${r.referrerName}', '${r.channel}',
      '', '', '${r.utmCampaign}', '', '',
      '', '', '${r.country}', '', '',
      '', '', '${r.deviceType}', 0,
      'pageview', 'pageview', ${r.revenue}, ${r.isGoal}, 1,
      [], [], '', 0
    )`.replace(/\s+/g, ' ').trim();
  });

  const cols = [
    'site_id', 'time', 'user_id_hash', 'cookie_id', 'visitor_hash',
    'hostname', 'pathname', 'title', 'referrer', 'referrer_name', 'channel',
    'utm_source', 'utm_medium', 'utm_campaign', 'utm_content', 'utm_term',
    'province', 'city', 'country_code', 'isp', 'carrier',
    'os', 'browser', 'device_type', 'viewport_width',
    'event_type', 'event_name', 'event_value', 'is_goal', 'is_new',
    'prop_keys', 'prop_vals', 'user_segment', 'is_bot',
  ].join(', ');

  chExec(`INSERT INTO statnive.events_raw (${cols}) VALUES ${values.join(', ')}`);

  // OPTIMIZE FINAL on rollups — matches storagetest.WriteEvents. Makes
  // MV-propagated state query-able synchronously. Production never runs
  // this; e2e is a controlled fixture so the cost is acceptable.
  for (const tbl of [
    'statnive.hourly_visitors',
    'statnive.daily_pages',
    'statnive.daily_sources',
  ]) {
    try {
      chExec(`OPTIMIZE TABLE ${tbl} FINAL`);
    } catch (err) {
      // Non-fatal; merge will complete async.
      // eslint-disable-next-line no-console
      console.warn(`[e2e] OPTIMIZE ${tbl} warning:`, (err as Error).message);
    }
  }
}

function buildFixture(): SeedRow[] {
  // Deterministic fixture: 10 distinct visitors × 2 sites × varied
  // dimensions so Sources / Pages / Campaigns / Realtime / Filters all
  // have rows to render + CH-oracle to assert against.
  // siteA: 10 mobile + 10 desktop, 5 Organic + 15 Direct, top path /home
  // siteB: 5 mobile + 15 desktop, 10 Organic + 10 Social
  const rows: SeedRow[] = [];

  // Fresh visitor hashes per row so HLL counts are deterministic.
  const vhex = (siteID: number, n: number): string => {
    const hex = (siteID * 1000 + n).toString(16).padStart(32, '0');
    return hex;
  };

  const siteAEvents = [
    // Organic Search + mobile
    { path: '/home', referrer: 'google', channel: 'Organic Search', utm: '', device: 'mobile', country: 'IR', goal: 0, rev: 0, count: 5 },
    // Direct + mobile
    { path: '/home', referrer: '(direct)', channel: 'Direct', utm: '', device: 'mobile', country: 'IR', goal: 0, rev: 0, count: 5 },
    // Direct + desktop + goal
    { path: '/checkout', referrer: '(direct)', channel: 'Direct', utm: '', device: 'desktop', country: 'IR', goal: 1, rev: 500_000, count: 5 },
    // Direct + desktop + campaign
    { path: '/pricing', referrer: '(direct)', channel: 'Direct', utm: 'spring_promo', device: 'desktop', country: 'IR', goal: 0, rev: 0, count: 5 },
  ];

  let nA = 0;
  for (const g of siteAEvents) {
    for (let i = 0; i < g.count; i++) {
      nA++;
      rows.push({
        siteID: SITE_A,
        minutesAgo: 5 + (nA % 30),
        pathname: g.path,
        referrerName: g.referrer,
        channel: g.channel,
        utmCampaign: g.utm,
        deviceType: g.device,
        country: g.country,
        visitorHex: vhex(SITE_A, nA),
        isGoal: g.goal,
        revenue: g.rev,
      });
    }
  }

  const siteBEvents = [
    // Organic Search + desktop
    { path: '/blog/a', referrer: 'google', channel: 'Organic Search', utm: '', device: 'desktop', country: 'US', goal: 0, rev: 0, count: 10 },
    // Social + mobile
    { path: '/home', referrer: 'twitter.com', channel: 'Social Media', utm: '', device: 'mobile', country: 'DE', goal: 0, rev: 0, count: 5 },
    // Social + desktop + goal
    { path: '/signup', referrer: 'linkedin.com', channel: 'Social Media', utm: 'winter_drive', device: 'desktop', country: 'DE', goal: 1, rev: 300_000, count: 5 },
  ];

  let nB = 0;
  for (const g of siteBEvents) {
    for (let i = 0; i < g.count; i++) {
      nB++;
      rows.push({
        siteID: SITE_B,
        minutesAgo: 5 + (nB % 30),
        pathname: g.path,
        referrerName: g.referrer,
        channel: g.channel,
        utmCampaign: g.utm,
        deviceType: g.device,
        country: g.country,
        visitorHex: vhex(SITE_B, nB),
        isGoal: g.goal,
        revenue: g.rev,
      });
    }
  }

  return rows;
}

async function waitForHealthz(port: number, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  const url = `http://127.0.0.1:${port}/healthz`;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url);
      if (res.ok) return;
    } catch {
      // fetch throws while binary is still booting; swallow.
    }
    await sleep(200);
  }
  throw new Error(`/healthz never responded within ${timeoutMs}ms`);
}

export default async function globalSetup(): Promise<void> {
  // Pre-flight: docker-compose ClickHouse must be up.
  try {
    chExec('SELECT 1');
  } catch (err) {
    throw new Error(
      `ClickHouse (${CH_CONTAINER}) not reachable via docker exec — run: docker compose -f deploy/docker-compose.dev.yml up -d clickhouse\n${(err as Error).message}`,
    );
  }

  // Binary must exist. CI runs `make build` first; local dev must too.
  if (!existsSync(BIN_PATH)) {
    throw new Error(
      `bin/statnive-live missing — run 'make build' first so the SPA dist embedded in the binary matches the source tree.`,
    );
  }

  const work = mkdtempSync(join(tmpdir(), 'statnive-e2e-'));
  const masterKey = join(work, 'master.key');
  const walDir = join(work, 'wal');
  const auditPath = join(work, 'audit.jsonl');

  // 32 random bytes → hex → 0600 (binary rejects looser perms).
  writeFileSync(masterKey, randomBytes(32).toString('hex'));
  chmodSync(masterKey, 0o600);

  // Clean + seed sites BEFORE spawning binary (table exists from prior runs;
  // first run creates it via migration during binary boot).
  // We tolerate failures on the first pre-boot clean.
  cleanSite(SITE_A, HOST_A);
  cleanSite(SITE_B, HOST_B);

  const env: NodeJS.ProcessEnv = {
    ...process.env,
    STATNIVE_SERVER_LISTEN: `127.0.0.1:${PORT}`,
    STATNIVE_MASTER_SECRET_PATH: masterKey,
    STATNIVE_INGEST_WAL_DIR: walDir,
    STATNIVE_AUDIT_PATH: auditPath,
    STATNIVE_CLICKHOUSE_ADDR: CH_ADDR,
    STATNIVE_DASHBOARD_SPA_ENABLED: 'true',
    STATNIVE_DASHBOARD_BEARER_TOKEN: BEARER,
    // Phase 2b — allow Secure=false session cookie for e2e (no TLS on
    // localhost) and seed a first-run admin for the Playwright login
    // flow to sign in as. Mirrors test/smoke/harness.sh exactly.
    STATNIVE_DEV: '1',
    STATNIVE_AUTH_SESSION_SECURE: 'false',
    STATNIVE_BOOTSTRAP_ADMIN_EMAIL: process.env.STATNIVE_E2E_ADMIN_EMAIL ?? 'e2e-admin@statnive.live',
    STATNIVE_BOOTSTRAP_ADMIN_PASSWORD: process.env.STATNIVE_E2E_ADMIN_PASSWORD ?? 'e2e-P@ssw0rd-static',
  };

  const logChunks: Buffer[] = [];
  // cwd = REPO_ROOT so the binary picks up ./config/sources.yaml with
  // its default relative path (same as smoke harness's invocation).
  const child = spawn(BIN_PATH, [], {
    env,
    cwd: REPO_ROOT,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  child.stdout?.on('data', (c) => logChunks.push(c));
  child.stderr?.on('data', (c) => logChunks.push(c));

  // Early-death surface: if binary exits during boot, dump captured
  // stdout+stderr and fail fast — same pattern as smoke harness.
  const earlyDeath = new Promise<never>((_, rej) => {
    child.once('exit', (code) => {
      rej(
        new Error(
          `statnive-live exited during boot (code=${code}):\n${Buffer.concat(logChunks).toString('utf8')}`,
        ),
      );
    });
  });

  try {
    await Promise.race([waitForHealthz(PORT, 30_000), earlyDeath]);
  } catch (err) {
    child.kill('SIGKILL');
    throw err;
  }

  // Now that migrations have run (binary boots + applies them), seed
  // sites + events.
  seedSite(SITE_A, HOST_A, 'Asia/Tehran');
  seedSite(SITE_B, HOST_B, 'UTC');
  const fixture = buildFixture();
  seedEvents(fixture);

  // Diagnostic: confirm rollups populated. Without this, silent seed
  // failures surface only as "panel stuck on loading…" later, which
  // wastes 10-30 min of CI flake triage before anyone looks here.
  const countRollup = (table: string): number => {
    const raw = chExec(`SELECT count() FROM ${table} WHERE site_id IN (${SITE_A}, ${SITE_B})`).trim();
    return Number(raw) || 0;
  };
  const eventsCount = countRollup('statnive.events_raw');
  const dailyPagesCount = countRollup('statnive.daily_pages');
  const hourlyVisitorsCount = countRollup('statnive.hourly_visitors');
  const dailySourcesCount = countRollup('statnive.daily_sources');
  // eslint-disable-next-line no-console
  console.log(
    `[e2e] seeded: ${fixture.length} rows → events_raw=${eventsCount}, ` +
    `daily_pages=${dailyPagesCount}, hourly_visitors=${hourlyVisitorsCount}, daily_sources=${dailySourcesCount}`,
  );
  if (eventsCount < fixture.length) {
    throw new Error(`events_raw seed short: inserted=${fixture.length} got=${eventsCount}`);
  }

  // Export to tests.
  process.env.STATNIVE_E2E_BASEURL = `http://127.0.0.1:${PORT}`;
  process.env.STATNIVE_E2E_PORT = String(PORT);
  process.env.STATNIVE_E2E_TOKEN = BEARER;
  process.env.STATNIVE_E2E_SITE_A = String(SITE_A);
  process.env.STATNIVE_E2E_SITE_B = String(SITE_B);
  process.env.STATNIVE_E2E_HOST_A = HOST_A;
  process.env.STATNIVE_E2E_HOST_B = HOST_B;
  process.env.STATNIVE_E2E_CH_CONTAINER = CH_CONTAINER;
  process.env.STATNIVE_E2E_ADMIN_EMAIL =
    process.env.STATNIVE_E2E_ADMIN_EMAIL ?? 'e2e-admin@statnive.live';
  process.env.STATNIVE_E2E_ADMIN_PASSWORD =
    process.env.STATNIVE_E2E_ADMIN_PASSWORD ?? 'e2e-P@ssw0rd-static';
  process.env.STATNIVE_E2E_PID = String(child.pid);

  // Detach stdout/stderr listeners once we're up — otherwise Playwright's
  // console will fill with the binary's structured JSON logs.
  child.stdout?.removeAllListeners('data');
  child.stderr?.removeAllListeners('data');

  // eslint-disable-next-line no-console
  console.log(`[e2e] statnive-live up at http://127.0.0.1:${PORT} (pid=${child.pid}, siteA=${SITE_A}, siteB=${SITE_B})`);
}
