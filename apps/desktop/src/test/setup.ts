import '@testing-library/jest-dom/vitest'
import { afterEach } from 'vitest'
import { cleanup, configure } from '@testing-library/react'

// Same reason as apps/web: Testing Library's one-second default for
// findBy*/waitFor is too tight for the CI runner, and the failure looks like a
// flaky test rather than a slow one. Costs nothing while tests pass.
configure({ asyncUtilTimeout: 5000 })

afterEach(() => {
  cleanup()
})

// Node >= 25 ships its own `localStorage`/`sessionStorage` globals. Without
// --localstorage-file they are unusable (undefined), and they shadow the
// jsdom implementation the tests expect. Put jsdom's Web Storage back.
for (const name of ['localStorage', 'sessionStorage'] as const) {
  if (typeof globalThis[name]?.clear !== 'function') {
    const jsdomWindow = (globalThis as unknown as { jsdom?: { window: Window } }).jsdom?.window
    if (jsdomWindow) {
      Object.defineProperty(globalThis, name, { configurable: true, value: jsdomWindow[name] })
    }
  }
}
