import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join, resolve } from 'node:path';

// Fast-gate air-gap guard. `make web-airgap-grep` scans the built
// dist/ for CDN URLs at CI time, but catching violations in source
// here shortens the feedback loop to <1s during `npm run test`.
//
// Rules:
//   - No url(http://), url(https://), url(//) anywhere in `web/src/**/*.css`
//     or `web/src/**/*.tsx` (any <link href="http...">).
//   - @fontsource imports are allowed — they resolve through
//     node_modules at build time and Vite bundles locally.

const __dirname = dirname(fileURLToPath(import.meta.url));
const SRC = resolve(__dirname, '..');

function walk(dir: string, files: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const s = statSync(full);
    if (s.isDirectory()) {
      if (entry === 'node_modules' || entry === '__tests__') continue;
      walk(full, files);
    } else if (/\.(css|tsx?|mts?)$/.test(entry)) {
      files.push(full);
    }
  }
  return files;
}

const SOURCE_FILES = walk(SRC);

describe('air-gap: no CDN / remote url()', () => {
  for (const file of SOURCE_FILES) {
    it(`${file.replace(SRC, 'src')} has no remote url()`, () => {
      const body = readFileSync(file, 'utf8');

      // Reject url() pointing at http:, https:, // (protocol-relative).
      expect(body).not.toMatch(/url\(\s*['"]?https?:/);
      expect(body).not.toMatch(/url\(\s*['"]?\/\//);

      // Reject raw <link href="http..."> in TSX.
      expect(body).not.toMatch(/href\s*=\s*['"]https?:/);
      expect(body).not.toMatch(/src\s*=\s*['"]https?:/);
    });
  }

  it('finds a non-empty source tree', () => {
    expect(SOURCE_FILES.length).toBeGreaterThan(0);
  });
});
