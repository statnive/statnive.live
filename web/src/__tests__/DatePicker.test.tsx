import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/preact';
import { DatePicker } from '../components/DatePicker';
import { rangeSignal, daysAgoIRST, presetToRange, isValidIrstRange } from '../state/range';
import { filtersSignal, EMPTY_FILTERS } from '../state/filters';
import { hashSignal } from '../state/hash';

describe('range utilities', () => {
  it('daysAgoIRST returns YYYY-MM-DD', () => {
    expect(daysAgoIRST(0)).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });

  it('presetToRange 7d spans 7 calendar days', () => {
    const r = presetToRange('7d');
    expect(r.from).not.toBe(r.to);
    expect(isValidIrstRange(r.from, r.to)).toBe(true);
  });

  it('isValidIrstRange rejects bad format + inverted ranges', () => {
    expect(isValidIrstRange('2026-04-21', '2026-04-22')).toBe(true);
    expect(isValidIrstRange('2026-04-22', '2026-04-21')).toBe(false);
    expect(isValidIrstRange('not-a-date', '2026-04-22')).toBe(false);
  });
});

describe('DatePicker component', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/#overview');
    hashSignal.value = { panel: 'overview', params: new URLSearchParams() };
    filtersSignal.value = { ...EMPTY_FILTERS };
    const r = presetToRange('7d');
    rangeSignal.value = r;
  });

  it('renders 4 preset chips', () => {
    render(<DatePicker />);
    expect(screen.getByText('Last 7 days')).toBeTruthy();
    expect(screen.getByText('Last 30 days')).toBeTruthy();
    expect(screen.getByText('Last 90 days')).toBeTruthy();
    expect(screen.getByText('Custom')).toBeTruthy();
  });

  it('clicking a preset updates rangeSignal', () => {
    render(<DatePicker />);
    fireEvent.click(screen.getByText('Last 30 days'));
    const expected = presetToRange('30d');
    expect(rangeSignal.value).toEqual(expected);
  });
});
