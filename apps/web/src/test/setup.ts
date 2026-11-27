import '@testing-library/jest-dom/vitest'
import { afterEach } from 'vitest'
import { cleanup, configure } from '@testing-library/react'

// Testing Library gives every findBy*/waitFor one second by default. That is
// enough on a developer machine and not enough on the CI runner, where a page
// doing two chained fetches regularly needs longer — the whole suite passes
// locally and drops a different test on every other CI run (ImageGalleryDialog,
// PreviewDialog, FilesContextMenu so far, all with the same signature: the
// query times out and the error dumps a DOM that simply had not finished
// rendering).
//
// Raising the budget costs nothing when tests pass: waitFor polls and returns
// the moment its condition holds. It only makes a genuinely broken test take
// longer to report.
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
