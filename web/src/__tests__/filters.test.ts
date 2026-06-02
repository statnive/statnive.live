import { describe, it, expect, beforeEach } from 'vitest';
import {
  filtersSignal,
  filtersToQuery,
  queryToFilters,
  updateFilters,
  clearFilters,
  setPropFilter,
  removePropFilter,
  hasAnyPropFilter,
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

// Segments Phase 3 — prop-scope filters round-trip through the URL hash
// via repeated `hit_prop`/`session_prop`/`user_prop` params. Each value
// is "name:value" so the server's parseScopedProps recovers the pair
// by splitting on the first colon.
describe('scoped prop filters', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/#overview');
    hashSignal.value = { panel: 'overview', params: new URLSearchParams() };
    filtersSignal.value = { ...EMPTY_FILTERS, hitProps: {}, sessionProps: {}, userProps: {} };
  });

  it('round-trips hit/session/user props through filtersToQuery + queryToFilters', () => {
    const f = {
      ...EMPTY_FILTERS,
      hitProps: { button: 'hero' },
      sessionProps: { ab_variant: 'B' },
      userProps: { plan: 'pro', signup_year: '2026' },
    };
    const q = filtersToQuery(f);
    const back = queryToFilters(q);
    expect(back.hitProps).toEqual({ button: 'hero' });
    expect(back.sessionProps).toEqual({ ab_variant: 'B' });
    expect(back.userProps).toEqual({ plan: 'pro', signup_year: '2026' });
  });

  it('setPropFilter adds + writes the URL hash', () => {
    setPropFilter('sessionProps', 'ab_variant', 'B');
    expect(filtersSignal.value.sessionProps).toEqual({ ab_variant: 'B' });
    expect(window.location.hash).toContain('session_prop=ab_variant%3AB');
  });

  it('removePropFilter deletes a single entry', () => {
    setPropFilter('userProps', 'plan', 'pro');
    setPropFilter('userProps', 'tier', 'gold');
    removePropFilter('userProps', 'plan');
    expect(filtersSignal.value.userProps).toEqual({ tier: 'gold' });
  });

  it('hasAnyPropFilter reports true when any scope is non-empty', () => {
    expect(hasAnyPropFilter(filtersSignal.value)).toBe(false);
    setPropFilter('hitProps', 'button', 'hero');
    expect(hasAnyPropFilter(filtersSignal.value)).toBe(true);
  });

  it('queryToFilters splits "name:value" on the FIRST colon (values may contain colons)', () => {
    const q = new URLSearchParams('user_prop=url:https%3A%2F%2Fexample.com');
    const f = queryToFilters(q);
    expect(f.userProps).toEqual({ url: 'https://example.com' });
  });

  it('clearFilters resets all three scope maps', () => {
    setPropFilter('hitProps', 'button', 'hero');
    setPropFilter('sessionProps', 'ab', 'B');
    setPropFilter('userProps', 'plan', 'pro');
    clearFilters();
    expect(filtersSignal.value.hitProps).toEqual({});
    expect(filtersSignal.value.sessionProps).toEqual({});
    expect(filtersSignal.value.userProps).toEqual({});
  });
});
