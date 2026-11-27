import { describe, expect, it } from 'vitest'
import {
  compareVersions,
  formatExactBytes,
  formatHumanBytes,
  formatTimestamp,
  isPreviewableImage,
} from './conflictCompare'

describe('isPreviewableImage', () => {
  it('accepts browser-renderable images regardless of case', () => {
    for (const p of ['/a/IMG_1.JPG', 'x.jpeg', 'c.png', 'd.WebP', 'e.gif', 'f.bmp', 'g.avif']) {
      expect(isPreviewableImage(p), p).toBe(true)
    }
  })

  it('rejects formats a webview cannot show and extensionless paths', () => {
    for (const p of ['scan.tif', 'scan.tiff', 'raw.cr2', 'doc.pdf', 'noext', '/dir.jpg/file']) {
      expect(isPreviewableImage(p), p).toBe(false)
    }
  })
})

describe('size formatting', () => {
  it('prints the exact byte count with locale grouping', () => {
    expect(formatExactBytes(1234567, 'de')).toBe('1.234.567 Byte')
    expect(formatExactBytes(1234567, 'en')).toBe('1,234,567 bytes')
  })

  it('prints a rounded size for orientation', () => {
    expect(formatHumanBytes(512, 'en')).toBe('512 B')
    expect(formatHumanBytes(1536, 'en')).toBe('1.5 KB')
    expect(formatHumanBytes(1234567, 'de')).toBe('1,2 MB')
  })
})

describe('formatTimestamp', () => {
  it('includes seconds', () => {
    expect(formatTimestamp('2026-11-03T14:05:09Z', 'en')).toMatch(/:09/)
  })

  it('returns an empty string for an invalid date', () => {
    expect(formatTimestamp('nope', 'en')).toBe('')
  })
})

describe('compareVersions', () => {
  const base = { sizeBytes: 100, modifiedAt: '2026-11-03T14:05:09.100Z' }

  it('treats equal size and a time within the same second as identical', () => {
    const r = compareVersions(base, { sizeBytes: 100, modifiedAt: '2026-11-03T14:05:09.900Z' })
    expect(r).toEqual({ size: false, modified: false, identical: true })
  })

  it('flags a differing size', () => {
    const r = compareVersions(base, { sizeBytes: 101, modifiedAt: base.modifiedAt })
    expect(r).toEqual({ size: true, modified: false, identical: false })
  })

  it('flags a differing date', () => {
    const r = compareVersions(base, { sizeBytes: 100, modifiedAt: '2026-11-03T14:05:10Z' })
    expect(r).toEqual({ size: false, modified: true, identical: false })
  })

  it('ignores dates entirely when they are not comparable', () => {
    const r = compareVersions(base, { sizeBytes: 100, modifiedAt: '2030-01-01T00:00:00Z' }, false)
    expect(r).toEqual({ size: false, modified: false, identical: false })
  })

  it('never calls versions identical while a fact is unknown', () => {
    const r = compareVersions(base, { sizeBytes: 100, modifiedAt: null })
    expect(r).toEqual({ size: false, modified: false, identical: false })
  })
})
