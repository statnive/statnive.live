import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

// Regression guard: every CSS custom property in tokens.css MUST match
// the swatch table in docs/brand.md. If anything drifts the brand-grep
// CI gate would still pass (because tokens.css is the one allowlisted
// hex source), so this test is the only guard that the values themselves
// are correct.

const __dirname = dirname(fileURLToPath(import.meta.url));
const tokens = readFileSync(resolve(__dirname, '../tokens.css'), 'utf8');

interface Expected {
  name: string;
  value: string;
}

const EXPECTED: Expected[] = [
  { name: '--paper', value: '#F4EFE6' },
  { name: '--ink', value: '#1A1916' },
  { name: '--rule-soft', value: '#C9C0AB' },
  { name: '--green', value: '#00756A' },
  { name: '--green-dk', value: '#004F48' },
  { name: '--green-lt', value: '#9FCDC5' },
  { name: '--navy', value: '#1E3551' },
  { name: '--ochre', value: '#B87B1A' },
  { name: '--plum', value: '#5F3B6E' },
  { name: '--rust', value: '#A84628' },
];

describe('brand tokens (docs/brand.md)', () => {
  for (const { name, value } of EXPECTED) {
    it(`${name} = ${value}`, () => {
      const re = new RegExp(`${name}:\\s*${value}\\b`, 'i');
      expect(tokens).toMatch(re);
    });
  }

  it('font tokens reference Fraunces + IBM Plex Sans + IBM Plex Mono', () => {
    expect(tokens).toMatch(/--serif:\s*'Fraunces'/);
    expect(tokens).toMatch(/--sans:\s*'IBM Plex Sans'/);
    expect(tokens).toMatch(/--mono:\s*'IBM Plex Mono'/);
  });

  it('all tokens scoped to #statnive-app (no :root leak)', () => {
    // The reset.css uses #statnive-app too; tokens.css MUST also scope
    // so the SPA can never bleed into adjacent embedded surfaces.
    expect(tokens).toMatch(/#statnive-app\s*\{/);
    expect(tokens).not.toMatch(/^:root\s*\{/m);
  });
});
