import { vi } from 'vitest'

type ChangeListener = (e: { matches: boolean }) => void

/**
 * jsdom has no matchMedia — stub it with a controllable media-query list
 * so theme tests can simulate OS light/dark switches.
 */
export function stubMatchMedia(initialDark: boolean) {
  let dark = initialDark
  const listeners = new Set<ChangeListener>()
  const mql = {
    get matches() { return dark },
    media: '(prefers-color-scheme: dark)',
    addEventListener: (_: string, cb: ChangeListener) => listeners.add(cb),
    removeEventListener: (_: string, cb: ChangeListener) => listeners.delete(cb),
  }
  vi.stubGlobal('matchMedia', vi.fn(() => mql))
  return {
    setSystemDark(value: boolean) {
      dark = value
      listeners.forEach((cb) => cb({ matches: value }))
    },
  }
}
