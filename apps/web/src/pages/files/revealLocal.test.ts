import { describe, it, expect, vi } from 'vitest'
import { buildRevealUrl, revealLocally } from './revealLocal'

describe('buildRevealUrl', () => {
  it('baut privasync://reveal URL mit encodetem Pfad', () => {
    expect(buildRevealUrl('abc-123', '/docs/Report 1.pdf')).toBe(
      'privasync://reveal?root=abc-123&path=%2Fdocs%2FReport%201.pdf',
    )
  })

  it('encodet Sonderzeichen in root und path', () => {
    expect(buildRevealUrl('a&b', '/ä/#1.txt')).toBe(
      'privasync://reveal?root=a%26b&path=%2F%C3%A4%2F%231.txt',
    )
  })
})

describe('revealLocally', () => {
  it('klickt einen unsichtbaren Link mit der Reveal-URL an', () => {
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    revealLocally('root-1', '/a.txt')
    expect(clickSpy).toHaveBeenCalledOnce()
    const anchor = clickSpy.mock.instances[0] as HTMLAnchorElement
    expect(anchor.href).toBe('privasync://reveal?root=root-1&path=%2Fa.txt')
    clickSpy.mockRestore()
  })
})
