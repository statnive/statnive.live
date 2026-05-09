import { describe, it, expect } from 'vitest';
import { fmtMoney, fmtInt, fmtPct } from '../lib/fmt';

// Currency is a display-only label in this codebase — the stored
// integer is the major unit (no cents-division). These tests pin
// the no-fraction-digits invariant: 150_000 with EUR/USD/GBP/JPY/IRR
// must all render with the integer intact, varying only by symbol.

describe('fmtMoney — currency as display label', () => {
  it('EUR renders as €150,000 (no cents-division)', () => {
    expect(fmtMoney(150000, 'EUR')).toBe('€150,000');
  });

  it('USD renders as $150,000', () => {
    expect(fmtMoney(150000, 'USD')).toBe('$150,000');
  });

  it('GBP renders as £150,000', () => {
    expect(fmtMoney(150000, 'GBP')).toBe('£150,000');
  });

  it('JPY renders as ¥150,000 (zero-decimal currency)', () => {
    expect(fmtMoney(150000, 'JPY')).toBe('¥150,000');
  });

  it('IRR renders with the IRR code prefix and integer', () => {
    // Intl renders IRR as "IRR 150,000" or "IRR 150,000.00" depending
    // on engine; with maximumFractionDigits=0 we always get the
    // integer. The leading "IRR" string is what matters.
    const out = fmtMoney(150000, 'IRR');
    expect(out).toContain('IRR');
    expect(out).toContain('150,000');
  });

  it('zero amount renders with currency symbol', () => {
    expect(fmtMoney(0, 'EUR')).toBe('€0');
    expect(fmtMoney(0, 'JPY')).toBe('¥0');
  });

  it('large amounts thousand-separate correctly', () => {
    expect(fmtMoney(1500000, 'EUR')).toBe('€1,500,000');
    expect(fmtMoney(123456789, 'USD')).toBe('$123,456,789');
  });

  it('empty currency falls back to plain integer', () => {
    expect(fmtMoney(150000, '')).toBe('150,000');
  });

  it('invalid currency code falls back to "<int> <code>"', () => {
    // 'FOO' is not a valid ISO 4217 code; Intl.NumberFormat throws
    // RangeError, fmtMoney catches and uses the suffix fallback.
    const out = fmtMoney(150000, 'FOO');
    expect(out).toContain('150,000');
    expect(out).toContain('FOO');
  });
});

describe('existing helpers stay intact', () => {
  it('fmtInt thousand-separates with en-US locale', () => {
    expect(fmtInt(1500000)).toBe('1,500,000');
  });

  it('fmtPct prints two decimals + percent sign', () => {
    expect(fmtPct(12.345)).toBe('12.35%');
  });
});
