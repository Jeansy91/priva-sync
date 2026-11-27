import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { FontViewer } from './FontViewer'
import { FontViewerDialog } from './FontViewerDialog'
import { apiClient } from '../../store/auth'
import type { FileSource } from './fileSource'

const rootSrc: FileSource = { kind: 'root', rootID: 'r-1' }

// jsdom has no FontFace implementation — stub it so FontViewer's happy path
// and its ArrayBuffer -> url() fallback can both be exercised deterministically.
let failFirstLoad = false
let loadCalls = 0

class FakeFontFace {
  family: string
  source: unknown
  constructor(family: string, source: unknown) {
    this.family = family
    this.source = source
  }
  load() {
    loadCalls++
    if (failFirstLoad && loadCalls === 1) return Promise.reject(new Error('bad arraybuffer'))
    return Promise.resolve(this)
  }
}

class AlwaysFailFontFace {
  constructor(public family: string, public source: unknown) {}
  load() {
    return Promise.reject(new Error('nope'))
  }
}

beforeEach(() => {
  vi.restoreAllMocks()
  failFirstLoad = false
  loadCalls = 0
  ;(globalThis as any).FontFace = FakeFontFace
  ;(globalThis as any).URL.createObjectURL = vi.fn(() => 'blob:mock-font')
  ;(globalThis as any).URL.revokeObjectURL = vi.fn()
})

describe('FontViewer', () => {
  it('laedt die Schrift aus dem ArrayBuffer und rendert Sample-Text in allen Groessen', async () => {
    const blob = new Blob(['fontbytes'], { type: 'font/ttf' })
    const { findByText, getByText } = renderWithI18n(
      <FontViewer blob={blob} name="MyFont.ttf" />,
      { lang: 'de' },
    )
    await findByText('MyFont')
    expect(getByText('MyFont.ttf')).toBeInTheDocument()
    expect(getByText('80 px')).toBeInTheDocument()
    expect(getByText('Zwölf Boxkämpfer jagen Viktor quer über den großen Sylter Deich')).toBeInTheDocument()
  })

  it('faellt bei einem ArrayBuffer-Ladefehler auf eine Blob-URL zurueck', async () => {
    failFirstLoad = true
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const blob = new Blob(['fontbytes'], { type: 'font/ttf' })
    const { findByText } = renderWithI18n(
      <FontViewer blob={blob} name="Retry.woff2" />,
      { lang: 'de' },
    )
    await findByText('Retry')
    expect(loadCalls).toBe(2)
    expect((globalThis as any).URL.createObjectURL).toHaveBeenCalledWith(blob)
  })

  it('zeigt eine Fehlermeldung, wenn beide Ladeversuche scheitern', async () => {
    ;(globalThis as any).FontFace = AlwaysFailFontFace
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const blob = new Blob(['fontbytes'], { type: 'font/ttf' })
    const { findByText } = renderWithI18n(
      <FontViewer blob={blob} name="Broken.ttf" />,
      { lang: 'de' },
    )
    await findByText(/Schrift konnte nicht geladen werden/i)
  })
})

describe('FontViewerDialog', () => {
  it('laedt das Blob und delegiert an FontViewer', async () => {
    vi.spyOn(apiClient.files, 'download').mockResolvedValue({
      ok:   true,
      blob: () => Promise.resolve(new Blob(['x'], { type: 'font/ttf' })),
    } as any)
    const onClose = vi.fn()
    const { findAllByText, getByRole } = renderWithI18n(
      <FontViewerDialog source={rootSrc} path="/a.ttf" name="Dialog.ttf" onClose={onClose} />,
      { lang: 'de' },
    )
    // Both the dialog header and FontViewer's own filename label show the name.
    await vi.waitFor(async () => expect((await findAllByText('Dialog.ttf')).length).toBeGreaterThanOrEqual(2))
    fireEvent.click(getByRole('button'))
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('zeigt eine Fehlermeldung, wenn der Download fehlschlaegt', async () => {
    vi.spyOn(apiClient.files, 'download').mockResolvedValue({ ok: false } as any)
    const { findByText } = renderWithI18n(
      <FontViewerDialog source={rootSrc} path="/a.ttf" name="Fail.ttf" onClose={() => {}} />,
      { lang: 'de' },
    )
    await findByText(/Vorschau konnte nicht geladen werden/i)
  })
})
