import { describe, it, expect } from 'vitest';
import { tzShortLabel } from '../lib/tz';

// tz.ts derives the short timezone label for AppShell's
// .statnive-tz-chip via Intl.DateTimeFormat. These tests pin the
// DST-handling + fallback contract.

describe('tzShortLabel', () => {
  it('returns a CEST-equivalent label for Europe/Berlin in mid-summer', () => {
    // 2026-07-15 noon UTC = 14:00 CEST. Browser Intl tables return
    // "CEST"; Node's Intl tables return "GMT+2". Both are valid
    // representations of UTC+02:00. Accept either.
    const label = tzShortLabel('Europe/Berlin', new Date('2026-07-15T12:00:00Z'));
    expect(label).toMatch(/^(CEST|GMT\+2)$/);
  });

  it('returns a CET-equivalent label for Europe/Berlin in mid-winter', () => {
    // 2026-01-15 noon UTC = 13:00 CET. Browser → "CET"; Node → "GMT+1".
    const label = tzShortLabel('Europe/Berlin', new Date('2026-01-15T12:00:00Z'));
    expect(label).toMatch(/^(CET|GMT\+1)$/);
  });

  it('returns UTC for the UTC zone', () => {
    const label = tzShortLabel('UTC', new Date('2026-06-03T12:00:00Z'));
    expect(label).toBe('UTC');
  });

  it('falls back to UTC for an unknown / unparseable IANA name', () => {
    const label = tzShortLabel('Garbage/Notreal', new Date('2026-06-03T12:00:00Z'));
    expect(label).toBe('UTC');
  });

  it('falls back to UTC for an empty zone string', () => {
    const label = tzShortLabel('', new Date('2026-06-03T12:00:00Z'));
    expect(label).toBe('UTC');
  });

  it('returns a tz label for Asia/Tehran (IRST or GMT+3:30 per Intl tables)', () => {
    const label = tzShortLabel('Asia/Tehran', new Date('2026-06-03T12:00:00Z'));
    expect(label).toMatch(/^(IRST|GMT\+3:30)$/);
  });
});
