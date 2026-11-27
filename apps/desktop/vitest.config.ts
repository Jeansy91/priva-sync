import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@privasync/ui':         path.resolve(__dirname, '../../packages/ui/src/index.ts'),
      '@privasync/api-client': path.resolve(__dirname, '../../packages/api-client/src/index.ts'),
      '@privasync/i18n':       path.resolve(__dirname, '../../packages/i18n/src/index.ts'),
    },
  },
  test: {
    environment: 'jsdom',
    // CI runs eight coverage-instrumented workers next to other gates; the
    // 5 s default then trips a different slow test on every run.
    testTimeout: process.env.CI ? 20_000 : 5_000,
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'lcov'],
      all: true,
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/**/*.test.{ts,tsx}', 'src/test/**'],
      // Baseline measured 2026-08-02 (round 5): lines 85.9/stmts
      // 83.6/funcs 86.7/branches 74.6 -- AC target (70%) reached with buffer
      // via App.tsx (event wiring + bulk-delete modal) and tauri.ts (IPC
      // command/event wrappers), both previously untested at 0%.
      thresholds: { lines: 82, statements: 80, functions: 83, branches: 71 },
    },
  },
})
