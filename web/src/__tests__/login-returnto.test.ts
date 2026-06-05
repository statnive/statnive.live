import { describe, it, expect } from 'vitest';

import { safeReturnTo } from '../pages/Login';

// Open-redirect guard for the OAuth /authorize → login bounce. safeReturnTo must
// only ever return a same-origin relative path; anything that could navigate to
// another origin must collapse to '' (→ normal dashboard).
describe('safeReturnTo', () => {
  const origin = 'https://app.statnive.live';

  it('accepts a same-origin /authorize path with its query', () => {
    const search = '?return_to=' + encodeURIComponent('/authorize?response_type=code&client_id=x&state=y');
    expect(safeReturnTo(search, origin)).toBe('/authorize?response_type=code&client_id=x&state=y');
  });

  it('returns empty when return_to is absent', () => {
    expect(safeReturnTo('', origin)).toBe('');
    expect(safeReturnTo('?foo=bar', origin)).toBe('');
  });

  it('rejects open-redirect vectors', () => {
    const vectors = [
      'https://evil.example.com/x', // absolute external
      'http://evil.example.com',
      '//evil.example.com', // protocol-relative
      '/\\evil.example.com', // backslash protocol-relative trick
      '\\/evil.example.com',
      'javascript:alert(1)', // scheme, no leading slash
      'evil.example.com', // bare host
      ' /authorize', // leading space → not a valid leading slash
    ];
    for (const v of vectors) {
      const got = safeReturnTo('?return_to=' + encodeURIComponent(v), origin);
      expect(got, `vector ${v} must be rejected`).toBe('');
    }
  });

  it('strips a host even if the URL parser would keep same-origin', () => {
    // A fully-qualified same-origin URL is reduced to path+query (never returns
    // the absolute form), and a different origin is rejected outright.
    expect(safeReturnTo('?return_to=' + encodeURIComponent('https://evil.example.com/authorize'), origin)).toBe('');
  });
});
