import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'path'
import { readFileSync } from 'fs'

const { version } = JSON.parse(readFileSync(path.resolve(__dirname, 'package.json'), 'utf-8'))

export default defineConfig({
  plugins: [react()],
  define: {
    __APP_VERSION__: JSON.stringify(version),
  },
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
      // Baseline measured 2026-08-05 (Runde 6): lines 74.2/stmts
      // 70.4/funcs 68.7/branches 64.6 -- AC target (>=70% lines) reached.
      thresholds: { lines: 72, statements: 68, functions: 66, branches: 62 },
    },
  },
})
