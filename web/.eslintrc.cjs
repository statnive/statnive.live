module.exports = {
  root: true,
  env: { browser: true, es2022: true, node: true },
  parserOptions: { ecmaVersion: 2022, sourceType: 'module', ecmaFeatures: { jsx: true } },
  extends: ['eslint:recommended'],
  rules: {
    'no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
    'no-console': ['warn', { allow: ['warn', 'error'] }],
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
