import { describe, it, expect, beforeEach } from 'vitest';
import {
  filtersSignal,
  filtersToQuery,
  queryToFilters,
  updateFilters,
  clearFilters,
  EMPTY_FILTERS,
} from '../state/filters';
import { hashSignal } from '../state/hash';

describe('filters serialization', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/#overview');
    hashSignal.value = { panel: 'overview', params: new URLSearchParams() };
    filtersSignal.value = { ...EMPTY_FILTERS };
  });

  it('filtersToQuery omits empty fields', () => {
    const q = filtersToQuery({ ...EMPTY_FILTERS, device: 'mobile' });
    expect(q.toString()).toBe('device=mobile');
  });

  it('queryToFilters round-trips', () => {
    const f = { ...EMPTY_FILTERS, device: 'desktop', channel: 'Organic Search', path: '/blog' };
    const q = filtersToQuery(f);
    const back = queryToFilters(q);
    expect(back).toEqual(f);
  });

  it('queryToFilters parses URLSearchParams with extra unknown keys', () => {
    const q = new URLSearchParams('device=mobile&xyz=ignore');
    const f = queryToFilters(q);
    expect(f.device).toBe('mobile');
    expect('xyz' in f).toBe(false);
  });

  it('updateFilters merges into filtersSignal and writes URL hash', () => {
    updateFilters({ device: 'mobile' });
    expect(filtersSignal.value.device).toBe('mobile');
    expect(window.location.hash).toContain('device=mobile');
  });

  it('clearFilters resets every key', () => {
    updateFilters({ device: 'mobile', channel: 'Direct', path: '/blog' });
    clearFilters();
    expect(filtersSignal.value).toEqual(EMPTY_FILTERS);
    expect(window.location.hash).toBe('#overview');
  });
});
