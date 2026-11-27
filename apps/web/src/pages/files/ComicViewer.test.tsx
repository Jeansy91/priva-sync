import { describe, it, expect, vi, beforeEach } from 'vitest'
import JSZip from 'jszip'
import { fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { ComicViewer } from './ComicViewer'

// Real jszip (already a runtime dependency for CBZ extraction) builds actual
// test fixtures instead of mocking the extraction module — more realistic
// than stubbing internal ZIP structure by hand.
async function makeCbz(files: Record<string, string>): Promise<Blob> {
  const zip = new JSZip()
  for (const [name, content] of Object.entries(files)) zip.file(name, content)
  const buf = await zip.generateAsync({ type: 'arraybuffer' })
  return new Blob([buf], { type: 'application/zip' })
}

// The comic page <img> elements carry alt="" (decorative), which excludes
// them from the accessibility tree's "img" role — query the DOM directly.
function imgSrcs(container: HTMLElement): string[] {
  return [...container.querySelectorAll('img')].map((el) => el.getAttribute('src') ?? '')
}

beforeEach(() => {
  vi.restoreAllMocks()
  let n = 0
  ;(globalThis as any).URL.createObjectURL = vi.fn(() => `blob:page-${n++}`)
  ;(globalThis as any).URL.revokeObjectURL = vi.fn()
})

describe('ComicViewer — CBZ', () => {
  it('entpackt Bilder in natuerlicher Reihenfolge (p2 vor p10) und ignoriert Nicht-Bilder', async () => {
    const blob = await makeCbz({
      'page10.jpg': 'ten', 'page2.jpg': 'two', 'page1.jpg': 'one', 'readme.txt': 'skip me',
    })
    const { findByText, getByText } = renderWithI18n(<ComicViewer blob={blob} name="comic.cbz" />, { lang: 'de' })
    await findByText('1 / 3')
    expect(getByText('1 / 3')).toBeInTheDocument()
  })

  it('blaettert vor/zurueck und deaktiviert die Buttons an den Raendern', async () => {
    const blob = await makeCbz({ 'p1.jpg': 'a', 'p2.jpg': 'b', 'p3.jpg': 'c' })
    const { findByText, findByLabelText, getByText } = renderWithI18n(
      <ComicViewer blob={blob} name="comic.cbz" />, { lang: 'de' },
    )
    await findByText('1 / 3')
    const prevBtn = await findByLabelText('Vorherige Seite')
    const nextBtn = await findByLabelText('Nächste Seite')
    expect(prevBtn).toBeDisabled()
    expect(nextBtn).not.toBeDisabled()

    fireEvent.click(nextBtn)
    await vi.waitFor(() => expect(getByText('2 / 3')).toBeInTheDocument())
    fireEvent.click(nextBtn)
    await vi.waitFor(() => expect(getByText('3 / 3')).toBeInTheDocument())
    expect(nextBtn).toBeDisabled()

    fireEvent.click(prevBtn)
    await vi.waitFor(() => expect(getByText('2 / 3')).toBeInTheDocument())
  })

  it('Doppelseiten-Modus mit separater Titelseite bildet ab Seite 2 echte Spreads', async () => {
    const blob = await makeCbz({ 'p1.jpg': 'a', 'p2.jpg': 'b', 'p3.jpg': 'c' })
    const { findByText, findByLabelText, getByText } = renderWithI18n(
      <ComicViewer blob={blob} name="comic.cbz" />, { lang: 'de' },
    )
    await findByText('1 / 3')
    const doubleBtn = await findByLabelText('Doppelseiten')
    fireEvent.click(doubleBtn)
    expect(doubleBtn).toHaveAttribute('aria-pressed', 'true')
    // Cover-separate stays solo at index 0.
    expect(getByText('1 / 3')).toBeInTheDocument()

    const nextBtn = await findByLabelText('Nächste Seite')
    fireEvent.click(nextBtn)
    await vi.waitFor(() => expect(getByText('2-3 / 3')).toBeInTheDocument())
    expect(nextBtn).toBeDisabled()
  })

  it('kehrt die Anzeigereihenfolge im RTL-Modus um', async () => {
    const blob = await makeCbz({ 'p1.jpg': 'a', 'p2.jpg': 'b', 'p3.jpg': 'c' })
    const { findByText, findByLabelText, container } = renderWithI18n(
      <ComicViewer blob={blob} name="comic.cbz" />, { lang: 'de' },
    )
    await findByText('1 / 3')
    fireEvent.click(await findByLabelText('Doppelseiten'))
    fireEvent.click(await findByLabelText('Nächste Seite'))
    await findByText('2-3 / 3')
    await vi.waitFor(() => expect(container.querySelectorAll('img')).toHaveLength(2))

    const before = imgSrcs(container)

    const rtlBtn = await findByLabelText('Rechts-nach-Links (Manga)')
    fireEvent.click(rtlBtn)
    expect(rtlBtn).toHaveAttribute('aria-pressed', 'true')

    const after = imgSrcs(container)
    expect(after).toEqual([...before].reverse())
  })

  it('zeigt eine Fehlermeldung, wenn das Archiv keine Bilder enthaelt', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const blob = await makeCbz({ 'readme.txt': 'no images here' })
    const { findByText } = renderWithI18n(<ComicViewer blob={blob} name="empty.cbz" />, { lang: 'de' })
    await findByText(/Comic konnte nicht geladen werden/i)
  })
})
