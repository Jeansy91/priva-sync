import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    globals: true,
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'lcov'],
      all: true,
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/**/*.test.{ts,tsx}'],
      // Baseline measured 2026-07-30: only fileIcon.tsx/utils.ts
      // are tested, the rest of packages/ui (dialogs, switch, slider, ...) is not.
      thresholds: { lines: 4, statements: 5, functions: 2, branches: 7 },
    },
  },
})
