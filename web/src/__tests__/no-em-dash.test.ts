import { describe, expect, it } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

// Em-dash regression guard. The impeccable design system bans em dashes
// in user-facing copy (see /Users/.../.claude/skills/impeccable/SKILL.md
// shared design laws). This test scans every `.tsx` source file under
// `web/src/`, excluding code comments and test files, and fails if a
// space-em-dash-space sequence appears in displayable text. New panels
// that drift back to em-dash prose fail here before shipping.
//
// What this catches: prose em-dashes in JSX text nodes, string literals,
// and attribute values (title, aria-label, placeholder).
//
// What it deliberately doesn't catch:
// - Code comments (`//` line and `/* */` block — internal prose, not shipped)
// - Test files (this file and __tests__/ live outside the shipping bundle)
// - Single-character em-dash glyphs used as "missing value" placeholders
//   inside `{}` — those have been migrated to middle dot `·` separately.

const SRC = join(__dirname, '..');
const SKIP_DIRS = new Set(['__tests__']);
const EM_DASH = ' — '; // U+2014, surrounded by spaces

function walkTsx(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      if (SKIP_DIRS.has(entry)) continue;
      walkTsx(full, out);
    } else if (entry.endsWith('.tsx')) {
      out.push(full);
    }
  }
  return out;
}

function stripComments(source: string): string {
  // Strip /* ... */ block comments (greedy across newlines) and // line comments.
  // Conservative: handles the comment cases this codebase actually uses.
  return source
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('\n')
    .map((line) => line.replace(/\/\/.*$/, ''))
    .join('\n');
}

describe('no em-dash in user-facing copy', () => {
  const tsxFiles = walkTsx(SRC);

  it.each(tsxFiles)('%s has no " \\u2014 " in displayable strings', (path) => {
    const code = stripComments(readFileSync(path, 'utf-8'));
    const hits: { line: number; text: string }[] = [];
    code.split('\n').forEach((line, idx) => {
      if (line.includes(EM_DASH)) {
        hits.push({ line: idx + 1, text: line.trim() });
      }
    });
    expect(hits, `em-dash found in ${path}:\n${hits.map((h) => `  L${h.line}: ${h.text}`).join('\n')}`).toEqual([]);
  });
});
