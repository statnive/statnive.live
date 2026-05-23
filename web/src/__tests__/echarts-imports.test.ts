import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

// Tree-shake guardrail. Apache ECharts has a 100 KB gz full bundle
// and a ~30 KB gz tree-shaken custom build. The only way to keep the
// chart-chunk under budget is to import from the sub-paths
// (echarts/core, echarts/charts, echarts/components, echarts/renderers)
// and NEVER from bare 'echarts'.
//
// This test walks every .ts/.tsx source file under src/ and fails if
// any line contains a bare 'echarts' import. Belt-and-braces with the
// ESLint `no-restricted-imports` rule in .eslintrc.cjs (which only
// covers .js/.mjs in this project).
//
// Allowed sub-paths:
//   - echarts/core
//   - echarts/charts
//   - echarts/components
//   - echarts/renderers
//
// Forbidden patterns:
//   - import 'echarts'
//   - import * as echarts from 'echarts'
//   - import echarts from 'echarts'
//   - import { LineChart } from 'echarts'

const __dirname = dirname(fileURLToPath(import.meta.url));
const SRC = join(__dirname, '..');

const ALLOWED_SUBPATHS = new Set(['core', 'charts', 'components', 'renderers']);

function walkSrc(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules' || entry === 'dist') continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      walkSrc(full, out);
    } else if (entry.endsWith('.ts') || entry.endsWith('.tsx')) {
      out.push(full);
    }
  }
  return out;
}

// Match any `from 'echarts...'` or `from "echarts..."` import target.
// Capture group is the path AFTER 'echarts' (empty for bare imports,
// '/core' / '/charts' / etc. for sub-path imports).
const IMPORT_RE = /from\s+['"]echarts(\/[A-Za-z][A-Za-z0-9/_-]*)?['"]/g;

describe('echarts imports (tree-shake guardrail)', () => {
  const files = walkSrc(SRC);

  it.each(files)('%s only imports from echarts sub-paths (core, charts, components, renderers)', (path) => {
    const code = readFileSync(path, 'utf-8');
    const violations: { line: number; text: string }[] = [];

    code.split('\n').forEach((line, idx) => {
      // Skip comments — they may legitimately mention 'echarts'.
      const trimmed = line.trim();
      if (trimmed.startsWith('//') || trimmed.startsWith('*')) return;

      let m: RegExpExecArray | null;
      IMPORT_RE.lastIndex = 0;
      while ((m = IMPORT_RE.exec(line)) !== null) {
        const sub = m[1]; // e.g. '/core' or undefined for bare 'echarts'
        if (!sub) {
          violations.push({ line: idx + 1, text: trimmed });
          continue;
        }
        const segments = sub.slice(1).split('/');
        const root = segments[0];
        if (!ALLOWED_SUBPATHS.has(root)) {
          violations.push({ line: idx + 1, text: trimmed });
        }
      }
    });

    expect(
      violations,
      `bare or forbidden echarts import in ${path}:\n${violations
        .map((v) => `  L${v.line}: ${v.text}`)
        .join('\n')}`,
    ).toEqual([]);
  });
});
