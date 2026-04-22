// CH-oracle helper — Playwright's Tier-1 assertion mechanism.
// Wraps `docker exec clickhouse-client` with JSONEachRow parsing. Tests
// correlate dashboard UI values against raw ClickHouse rollup queries
// (CLAUDE.md § Testing ClickHouse-Oracle Assertion Hierarchy).
//
// Avoids pulling in @clickhouse/client (~200 KB devDep) — the queries
// are SQL strings, not programmatic builders, so execSync suffices.

import { execSync } from 'node:child_process';

function container(): string {
  return process.env.STATNIVE_E2E_CH_CONTAINER ?? 'statnive-clickhouse-dev';
}

// normalizeSQL collapses whitespace so JSON.stringify doesn't ship `\n`
// sequences through the shell layer (ClickHouse rejects them as
// SYNTAX_ERROR). Strip `--` line comments first — the collapse would
// otherwise fold the rest of the query into the comment.
function normalizeSQL(sql: string): string {
  return sql
    .split('\n')
    .map((line) => line.replace(/--.*$/, ''))
    .join(' ')
    .replace(/\s+/g, ' ')
    .trim();
}

// chExec runs any SQL against the dev CH container. Callers that want
// rows use chQuery / chQueryAll; callers that want raw stdout (DDL,
// INSERT, DELETE) invoke chExec directly.
export function chExec(sql: string, opts: { format?: string } = {}): string {
  const format = opts.format ? ` --format ${opts.format}` : '';
  return execSync(
    `docker exec ${container()} clickhouse-client --port 9000${format} -q ${JSON.stringify(normalizeSQL(sql))}`,
    { encoding: 'utf8' },
  );
}

function runCH(sql: string): string {
  return chExec(sql, { format: 'JSONEachRow' });
}

// chQueryAll returns every row as a parsed JSON object. Empty string
// output = no rows.
export function chQueryAll<T = Record<string, unknown>>(sql: string): T[] {
  const out = runCH(sql).trim();
  if (!out) return [];
  return out.split('\n').map((line) => JSON.parse(line) as T);
}

// chQuery returns the single row a query is expected to produce, or
// undefined if the result set is empty. Throws if multiple rows came
// back — the caller should use chQueryAll in that case.
export function chQuery<T = Record<string, unknown>>(sql: string): T | undefined {
  const rows = chQueryAll<T>(sql);
  if (rows.length === 0) return undefined;
  if (rows.length > 1) {
    throw new Error(`chQuery expected 0 or 1 row, got ${rows.length}: ${sql}`);
  }
  return rows[0];
}

// waitForCount polls a count() query until it reaches expected or the
// deadline expires. Used for rollup-materialization races.
export async function waitForCount(
  sql: string,
  expected: number,
  timeoutMs = 10_000,
): Promise<number> {
  const deadline = Date.now() + timeoutMs;
  let got = 0;
  while (Date.now() < deadline) {
    const row = chQuery<{ count: string }>(sql);
    got = row ? Number(row.count) : 0;
    if (got >= expected) return got;
    await new Promise((r) => setTimeout(r, 200));
  }
  return got;
}

// Helper that matches the dashboard Filter defaults — last 7 days IRST,
// returned as UTC RFC-3339 strings CH will accept as DateTime args.
// Tests use this to ensure their oracle query's window matches the
// panel's API call.
export function defaultWindow(): { from: string; to: string } {
  const nowIRST = Date.now() + (3 * 60 + 30) * 60 * 1000;
  const today = new Date(nowIRST).toISOString().slice(0, 10);
  const sevenAgo = new Date(nowIRST - 7 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
  return { from: sevenAgo, to: today };
}
