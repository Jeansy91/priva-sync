import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    globals: true,
    passWithNoTests: true,
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'lcov'],
      all: true,
      include: ['src/**/*.ts'],
      exclude: ['src/**/*.test.ts'],
      // No test files exist yet -- threshold 0 documents the current state
      //; follow-up: add first tests for this client.
      thresholds: { lines: 0, statements: 0, functions: 0, branches: 0 },
    },
  },
})
