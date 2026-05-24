import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/preact';
import { DatePicker } from '../components/DatePicker';
import {
  rangeSignal,
  daysAgoIRST,
  presetToRange,
  isValidIrstRange,
  addDayIRST,
} from '../state/range';
import { filtersSignal, EMPTY_FILTERS } from '../state/filters';
import { hashSignal } from '../state/hash';

// Pin "today IRST" so chip → range assertions don't drift across runs.
// 2026-05-24T12:00:00Z lands well inside the IRST day 2026-05-24.
const FIXED_NOW = new Date('2026-05-24T12:00:00Z');

describe('range utilities', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(FIXED_NOW);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('daysAgoIRST returns YYYY-MM-DD', () => {
    expect(daysAgoIRST(0)).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });

  it('presetToRange 7d spans 7 calendar days', () => {
    const r = presetToRange('7d');
    expect(r.from).not.toBe(r.to);
    expect(isValidIrstRange(r.from, r.to)).toBe(true);
  });

  it('presetToRange today is half-open [today, tomorrow)', () => {
    expect(presetToRange('today')).toEqual({
      from: '2026-05-24',
      to: '2026-05-25',
    });
  });

  it('presetToRange yesterday is half-open [yesterday, today)', () => {
    expect(presetToRange('yesterday')).toEqual({
      from: '2026-05-23',
      to: '2026-05-24',
    });
  });

  it('addDayIRST shifts by one calendar day by default', () => {
    expect(addDayIRST('2026-05-24')).toBe('2026-05-25');
    expect(addDayIRST('2026-05-24', -1)).toBe('2026-05-23');
    expect(addDayIRST('2026-02-28')).toBe('2026-03-01');
  });

  it('isValidIrstRange rejects bad format + inverted ranges', () => {
    expect(isValidIrstRange('2026-04-21', '2026-04-22')).toBe(true);
    expect(isValidIrstRange('2026-04-22', '2026-04-21')).toBe(false);
    expect(isValidIrstRange('not-a-date', '2026-04-22')).toBe(false);
  });
});

describe('DatePicker component', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(FIXED_NOW);
    window.history.replaceState(null, '', '/#overview');
    hashSignal.value = { panel: 'overview', params: new URLSearchParams() };
    filtersSignal.value = { ...EMPTY_FILTERS };
    rangeSignal.value = presetToRange('7d');
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('renders all 6 preset chips in order', () => {
    render(<DatePicker />);
    const labels = [
      'Today',
      'Yesterday',
      'Last 7 days',
      'Last 30 days',
      'Last 90 days',
      'Custom',
    ];
    for (const label of labels) {
      expect(screen.getByText(label)).toBeTruthy();
    }
  });

  it('clicking Today updates rangeSignal to {today, tomorrow}', () => {
    render(<DatePicker />);
    fireEvent.click(screen.getByText('Today'));
    expect(rangeSignal.value).toEqual({
      from: '2026-05-24',
      to: '2026-05-25',
    });
  });

  it('clicking Yesterday updates rangeSignal to {yesterday, today}', () => {
    render(<DatePicker />);
    fireEvent.click(screen.getByText('Yesterday'));
    expect(rangeSignal.value).toEqual({
      from: '2026-05-23',
      to: '2026-05-24',
    });
  });

  it('clicking Last 30 days uses the duration preset', () => {
    render(<DatePicker />);
    fireEvent.click(screen.getByText('Last 30 days'));
    expect(rangeSignal.value).toEqual(presetToRange('30d'));
  });

  it('rehydrates Today chip from URL params on mount', () => {
    rangeSignal.value = presetToRange('today');
    render(<DatePicker />);
    const todayChip = screen.getByText('Today').closest('button');
    expect(todayChip?.getAttribute('aria-pressed')).toBe('true');
  });

  it('rehydrates Yesterday chip from URL params on mount', () => {
    rangeSignal.value = presetToRange('yesterday');
    render(<DatePicker />);
    const yChip = screen.getByText('Yesterday').closest('button');
    expect(yChip?.getAttribute('aria-pressed')).toBe('true');
  });

  it('clicking Custom opens the popover (Loader shown until Cally resolves)', () => {
    render(<DatePicker />);
    fireEvent.click(screen.getByText('Custom'));
    const dialog = screen.getByRole('dialog', { name: /custom date range/i });
    expect(dialog).toBeTruthy();
    // Cally is dynamic-imported; Loader is the synchronous fallback.
    expect(dialog.querySelector('.statnive-loading')).toBeTruthy();
  });

  it('Custom popover shows Range active by default and toggles to Single', () => {
    render(<DatePicker />);
    fireEvent.click(screen.getByText('Custom'));
    const range = screen.getByText('Range');
    const single = screen.getByText('Single');
    expect(range.getAttribute('aria-pressed')).toBe('true');
    expect(single.getAttribute('aria-pressed')).toBe('false');
    fireEvent.click(single);
    expect(range.getAttribute('aria-pressed')).toBe('false');
    expect(single.getAttribute('aria-pressed')).toBe('true');
  });
});
