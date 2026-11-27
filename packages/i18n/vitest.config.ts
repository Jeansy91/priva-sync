import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    globals: true,
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'lcov'],
      all: true,
      include: ['src/**/*.ts'],
      exclude: ['src/**/*.test.ts'],
      // Baseline measured 2026-07-30: lines 63.2/stmts 69.6/funcs 60/branches 90.
      thresholds: { lines: 60, statements: 65, functions: 55, branches: 85 },
    },
  },
})
