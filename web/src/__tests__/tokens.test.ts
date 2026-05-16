import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

// Regression guard: every CSS custom property in tokens.css MUST match
// the swatch table in docs/brand.md (Phase 5e — operator-console
// redesign, navy/cream/green/amber palette sourced from
// jaan-to/outputs/detect/design/statnive-brand-guideline/statnive-live.html).
// If anything drifts the brand-grep CI gate would still pass (tokens.css
// is the one allowlisted hex source), so this test is the only guard
// that the values themselves are correct.

const __dirname = dirname(fileURLToPath(import.meta.url));
const tokens = readFileSync(resolve(__dirname, '../tokens.css'), 'utf8');
const fonts = readFileSync(resolve(__dirname, '../fonts.css'), 'utf8');

interface Expected {
  name: string;
  value: string;
}

// Phase 5e — operator-console palette. Surface whites + brand navy ink,
// teal primary (unchanged from 5d), amber Admin accent, channel
// sub-palette keyed to the 17-step mapper in internal/enrich/channel.go.
const EXPECTED: Expected[] = [
  { name: '--paper', value: '#F3EFE7' },
  { name: '--paper-2', value: '#FFFFFF' },
  { name: '--ink', value: '#0A2540' },
  { name: '--ink-2', value: '#12304F' },
  { name: '--cream', value: '#EDE3D1' },
  { name: '--rule-soft', value: '#E8E5DC' },
  { name: '--rule-hair', value: '#EFECE4' },
  { name: '--green', value: '#00A693' },
  { name: '--green-dk', value: '#007A6C' },
  { name: '--green-lt', value: '#B0D4CC' },
  { name: '--error', value: '#B0243A' },
  { name: '--error-dk', value: '#8A1D2E' },
  { name: '--amber', value: '#C47A0E' },
  { name: '--chart-visitors', value: '#0A2540' },
  { name: '--chart-revenue', value: '#00A693' },
  { name: '--chart-ochre', value: '#B87B1A' },
  { name: '--chart-plum', value: '#5F3B6E' },
  { name: '--chart-rust', value: '#A84628' },
  { name: '--ch-direct', value: '#00A693' },
  { name: '--ch-search', value: '#1A73E8' },
  { name: '--ch-social', value: '#1A1A1A' },
  { name: '--ch-email', value: '#7A4A6E' },
  { name: '--ch-referral', value: '#8B7355' },
  { name: '--ch-ai', value: '#0A2540' },
  { name: '--ch-paid', value: '#8A5508' },
];

describe('brand tokens (docs/brand.md — statnive-live operator console)', () => {
  for (const { name, value } of EXPECTED) {
    it(`${name} = ${value}`, () => {
      const re = new RegExp(`${name}:\\s*${value}\\b`, 'i');
      expect(tokens).toMatch(re);
    });
  }

  it('font tokens reference bundled families with system-ui fallback', () => {
    expect(tokens).toMatch(/--display:\s*'Space Grotesk'/);
    expect(tokens).toMatch(/--sans:\s*'DM Sans'/);
    expect(tokens).toMatch(/--mono:\s*'JetBrains Mono'/);
  });

  it('all tokens scoped to #statnive-app (no :root leak)', () => {
    // The reset.css uses #statnive-app too; tokens.css MUST also scope
    // so the SPA can never bleed into adjacent embedded surfaces.
    expect(tokens).toMatch(/#statnive-app\s*\{/);
    expect(tokens).not.toMatch(/^:root\s*\{/m);
  });
});

describe('fonts.css — @fontsource bundles', () => {
  // Each family shipped must come from @fontsource/* (npm-installed,
  // vendored into node_modules, Vite-bundled into /app/assets/*.woff2
  // at build). No @font-face with url(http…) / url(//…) — that would
  // violate the air-gap invariant.
  it('imports Space Grotesk 500 via @fontsource', () => {
    expect(fonts).toMatch(/@import\s+['"]@fontsource\/space-grotesk\/500\.css['"]/);
  });

  it('imports DM Sans 400 + 500 via @fontsource', () => {
    expect(fonts).toMatch(/@import\s+['"]@fontsource\/dm-sans\/400\.css['"]/);
    expect(fonts).toMatch(/@import\s+['"]@fontsource\/dm-sans\/500\.css['"]/);
  });

  it('imports JetBrains Mono 400 + 500 via @fontsource', () => {
    expect(fonts).toMatch(/@import\s+['"]@fontsource\/jetbrains-mono\/400\.css['"]/);
    expect(fonts).toMatch(/@import\s+['"]@fontsource\/jetbrains-mono\/500\.css['"]/);
  });

  it('contains no remote url() references (air-gap)', () => {
    // Paranoia check — the @fontsource packages resolve WOFF2 paths
    // relative to node_modules, Vite bundles locally. Raw remote URLs
    // in fonts.css would bypass that and escape the air gap.
    expect(fonts).not.toMatch(/url\((['"]?)https?:/);
    expect(fonts).not.toMatch(/url\((['"]?)\/\//);
  });
});
