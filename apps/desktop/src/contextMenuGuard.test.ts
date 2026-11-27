import { describe, it, expect, beforeEach, vi } from 'vitest'

describe('installContextMenuGuard', () => {
  beforeEach(() => {
    vi.resetModules()
  })

  it('prevents default on contextmenu events', async () => {
    const { installContextMenuGuard } = await import('./contextMenuGuard')
    installContextMenuGuard()
    const ev = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
    window.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)
  })

  it('is idempotent — repeated install attaches only one listener', async () => {
    let count = 0
    const orig = window.addEventListener
    window.addEventListener = function (type: string, ...rest: unknown[]) {
      if (type === 'contextmenu') count++
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return (orig as any).call(window, type, ...rest)
    } as typeof window.addEventListener
    try {
      const { installContextMenuGuard } = await import('./contextMenuGuard')
      installContextMenuGuard()
      installContextMenuGuard()
      installContextMenuGuard()
      expect(count).toBe(1)
    } finally {
      window.addEventListener = orig
    }
  })
})
