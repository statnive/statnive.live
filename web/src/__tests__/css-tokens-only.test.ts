import { describe, expect, it } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

// Type-size token enforcement. The optically-too-small text tier in the
// panel (10px / 11px mono caps reading as ~9.2px on screen) was the #1
// user complaint that prompted this refactor. tokens.css now exposes:
//
//   --text-label-sm:    11px      (was 10px)
//   --text-label-table: 12px      (table headers)
//   --text-meta:        12px      (was 11px)
//   --text-num-cell:    14px      (numeric table cells)
//   --text-num-primary: 36px      (KPI big numbers)
//
// This test asserts no CSS file under web/src/ reintroduces the old
// literals as raw `font-size: 10px;` / `font-size: 11px;` / `9px` /
// `0.75rem`. New work must route through the tokens.

const SRC = join(__dirname, '..');
const SKIP_FILES = new Set(['tokens.css']); // SoT itself
const BANNED = [
  { pattern: /\bfont-size:\s*10px\s*;/, label: 'font-size: 10px' },
  { pattern: /\bfont-size:\s*11px\s*;/, label: 'font-size: 11px' },
  { pattern: /\bfont-size:\s*9px\s*;/, label: 'font-size: 9px' },
  { pattern: /\bfont-size:\s*0\.75rem\s*;/, label: 'font-size: 0.75rem' },
];

function walkCss(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      walkCss(full, out);
    } else if (entry.endsWith('.css') && !SKIP_FILES.has(entry)) {
      out.push(full);
    }
  }
  return out;
}

describe('font-size literals route through tokens.css', () => {
  const cssFiles = walkCss(SRC);

  it.each(cssFiles)('%s has no banned font-size literals', (path) => {
    const code = readFileSync(path, 'utf-8');
    const hits: { line: number; text: string; banned: string }[] = [];
    code.split('\n').forEach((line, idx) => {
      for (const { pattern, label } of BANNED) {
        if (pattern.test(line)) {
          hits.push({ line: idx + 1, text: line.trim(), banned: label });
        }
      }
    });
    expect(hits, `banned literals in ${path}:\n${hits.map((h) => `  L${h.line} [${h.banned}]: ${h.text}`).join('\n')}`).toEqual([]);
  });
});
