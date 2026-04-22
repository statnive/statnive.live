import { describe, it, expect, beforeEach } from 'vitest';
import { parseHash, navigate, hashSignal, DEFAULT_PANEL } from '../state/hash';

describe('parseHash', () => {
  it('defaults empty hash to overview', () => {
    const s = parseHash('');
    expect(s.panel).toBe(DEFAULT_PANEL);
    expect(s.params.toString()).toBe('');
  });

  it('strips a leading #', () => {
    const s = parseHash('#sources');
    expect(s.panel).toBe('sources');
  });

  it('parses query params after ?', () => {
    const s = parseHash('#sources?device=mobile&from=2026-04-15');
    expect(s.panel).toBe('sources');
    expect(s.params.get('device')).toBe('mobile');
    expect(s.params.get('from')).toBe('2026-04-15');
  });

  it('falls back to overview on unknown panel', () => {
    const s = parseHash('#garbage');
    expect(s.panel).toBe('overview');
  });

  it('handles malformed hash with ? and no name', () => {
    const s = parseHash('#?x=1');
    expect(s.panel).toBe('overview');
    expect(s.params.get('x')).toBe('1');
  });
});

describe('navigate', () => {
  beforeEach(() => {
    // Reset location.hash before each test. jsdom treats hash assignment
    // as a real navigation — history.replaceState keeps the URL in sync
    // without triggering the hashchange event synchronously.
    window.history.replaceState(null, '', '/#overview');
    hashSignal.value = { panel: 'overview', params: new URLSearchParams() };
  });

  it('writes to location.hash + updates hashSignal', () => {
    navigate('sources');
    expect(window.location.hash).toBe('#sources');
    expect(hashSignal.value.panel).toBe('sources');
  });

  it('preserves params through navigation', () => {
    const p = new URLSearchParams({ device: 'mobile' });
    navigate('pages', p);
    expect(window.location.hash).toBe('#pages?device=mobile');
    expect(hashSignal.value.params.get('device')).toBe('mobile');
  });
});
