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

// Plugin brand guideline palette (Phase 5d) — warmer paper + brighter
// teal + iOS error palette. See
// jaan-to/outputs/detect/design/statnive-brand-guideline/
// statnive-plugin-brand-guidelines.html for the spec.
const EXPECTED: Expected[] = [
  { name: '--paper', value: '#EDE3D1' },
  { name: '--ink', value: '#1A1A1A' },
  { name: '--rule-soft', value: '#E8E5DC' },
  { name: '--green', value: '#00A693' },
  { name: '--green-dk', value: '#007A6C' },
  { name: '--green-lt', value: '#B0D4CC' },
  { name: '--error', value: '#FF3B30' },
  { name: '--error-dk', value: '#B0243A' },
  { name: '--navy', value: '#1E3551' },
  { name: '--ochre', value: '#B87B1A' },
  { name: '--plum', value: '#5F3B6E' },
  { name: '--rust', value: '#A84628' },
];

describe('brand tokens (docs/brand.md — plugin guideline)', () => {
  for (const { name, value } of EXPECTED) {
    it(`${name} = ${value}`, () => {
      const re = new RegExp(`${name}:\\s*${value}\\b`, 'i');
      expect(tokens).toMatch(re);
    });
  }

  it('font tokens prefer DM Sans / JetBrains Mono with IBM Plex fallback', () => {
    expect(tokens).toMatch(/--sans:\s*'DM Sans',\s*'IBM Plex Sans'/);
    expect(tokens).toMatch(/--mono:\s*'JetBrains Mono',\s*'IBM Plex Mono'/);
  });

  it('all tokens scoped to #statnive-app (no :root leak)', () => {
    // The reset.css uses #statnive-app too; tokens.css MUST also scope
    // so the SPA can never bleed into adjacent embedded surfaces.
    expect(tokens).toMatch(/#statnive-app\s*\{/);
    expect(tokens).not.toMatch(/^:root\s*\{/m);
  });
});
