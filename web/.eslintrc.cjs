module.exports = {
  root: true,
  env: { browser: true, es2022: true, node: true },
  parserOptions: { ecmaVersion: 2022, sourceType: 'module', ecmaFeatures: { jsx: true } },
  extends: ['eslint:recommended'],
  rules: {
    'no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
    'no-console': ['warn', { allow: ['warn', 'error'] }],
    // Tree-shake discipline: bare `import 'echarts'` pulls ~100 KB gz.
    // Only sub-path imports (echarts/core, echarts/charts, etc.) allowed.
    // The static-analysis test at web/src/__tests__/echarts-imports.test.ts
    // is the load-bearing guardrail for .ts/.tsx (this rule only catches
    // .js / .mjs drift).
    'no-restricted-imports': [
      'error',
      {
        paths: [
          { name: 'echarts', message: 'Import from echarts/core, echarts/charts, echarts/components, or echarts/renderers instead. Bare echarts pulls the full bundle.' },
        ],
      },
    ],
  },
  ignorePatterns: ['node_modules/', 'dist/'],
  overrides: [
    {
      // TypeScript uses `interface` / type syntax the default parser
      // doesn't understand; tsc + tsconfig.json's `strict: true` already
      // cover type correctness. Skip lint on .ts(x) for now — Phase 5b
      // can pair eslint with @typescript-eslint/parser when it's worth
      // the bundle-time cost.
      files: ['*.ts', '*.tsx'],
      rules: { 'no-unused-vars': 'off' },
      parserOptions: { ecmaVersion: 2022 },
    },
  ],
};
