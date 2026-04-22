// Playwright globalTeardown — kills the binary spawned by globalSetup
// and cleans the two seeded sites from CH so local re-runs start clean.
// CI's dashboard-e2e job also runs `docker compose down -v` afterward;
// this teardown keeps local dev tidy for the common case where the
// operator leaves the CH container running between invocations.

import { chExec } from './fixtures/chOracle';

async function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

function safeChExec(sql: string): void {
  try {
    chExec(sql);
  } catch {
    // Teardown best-effort: container may be down if CI has already torn
    // down docker-compose. Swallow and continue.
  }
}

export default async function globalTeardown(): Promise<void> {
  const pidStr = process.env.STATNIVE_E2E_PID;
  const pid = pidStr ? Number(pidStr) : NaN;

  if (Number.isFinite(pid) && pid > 0) {
    try {
      process.kill(pid, 'SIGTERM');
      for (let i = 0; i < 25; i++) {
        try {
          process.kill(pid, 0); // sig 0 = existence check
          await sleep(200);
        } catch {
          break; // already gone
        }
      }
      try {
        process.kill(pid, 'SIGKILL');
      } catch {
        // already exited
      }
    } catch {
      // Process may already be gone if the test run crashed.
    }
  }

  const siteA = Number(process.env.STATNIVE_E2E_SITE_A ?? 801);
  const siteB = Number(process.env.STATNIVE_E2E_SITE_B ?? 802);

  for (const siteID of [siteA, siteB]) {
    for (const tbl of [
      'statnive.events_raw',
      'statnive.hourly_visitors',
      'statnive.daily_pages',
      'statnive.daily_sources',
    ]) {
      safeChExec(`ALTER TABLE ${tbl} DELETE WHERE site_id = ${siteID} SETTINGS mutations_sync = 2`);
    }
    safeChExec(`ALTER TABLE statnive.sites DELETE WHERE site_id = ${siteID} SETTINGS mutations_sync = 2`);
  }
}
