import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { EpubViewer } from './EpubViewer'

const renditionMock = vi.hoisted(() => ({
  display: vi.fn().mockResolvedValue(undefined),
  prev:    vi.fn(),
  next:    vi.fn(),
  destroy: vi.fn(),
}))
const bookMock = vi.hoisted(() => ({
  renderTo: vi.fn(() => renditionMock),
  destroy:  vi.fn(),
}))
const ePubFactory = vi.hoisted(() => vi.fn(() => bookMock))

vi.mock('epubjs', () => ({ default: ePubFactory }))

beforeEach(() => {
  vi.clearAllMocks()
  ePubFactory.mockImplementation(() => bookMock)
  bookMock.renderTo.mockReturnValue(renditionMock)
  renditionMock.display.mockResolvedValue(undefined)
})

describe('EpubViewer', () => {
  it('rendert das Buch und blendet den Ladeindikator danach aus', async () => {
    const blob = new Blob(['epubbytes'], { type: 'application/epub+zip' })
    const { findByLabelText, queryByText } = renderWithI18n(
      <EpubViewer blob={blob} name="book.epub" />,
      { lang: 'de' },
    )
    await findByLabelText('Vorherige Seite')
    expect(bookMock.renderTo).toHaveBeenCalledOnce()
    expect(renditionMock.display).toHaveBeenCalledOnce()
    // The nav buttons render immediately; the indicator only goes once
    // display() has resolved.
    await waitFor(() => expect(queryByText(/Buch wird geladen/i)).not.toBeInTheDocument())
  })

  it('blaettert per Vor/Zurueck-Buttons ueber die Rendition', async () => {
    const blob = new Blob(['epubbytes'], { type: 'application/epub+zip' })
    const { findByLabelText } = renderWithI18n(
      <EpubViewer blob={blob} name="book.epub" />,
      { lang: 'de' },
    )
    const prevBtn = await findByLabelText('Vorherige Seite')
    const nextBtn = await findByLabelText('Nächste Seite')
    fireEvent.click(nextBtn)
    fireEvent.click(prevBtn)
    expect(renditionMock.next).toHaveBeenCalledOnce()
    expect(renditionMock.prev).toHaveBeenCalledOnce()
  })

  it('zeigt eine Fehlermeldung, wenn epubjs fehlschlaegt', async () => {
    ePubFactory.mockImplementation(() => { throw new Error('bad epub') })
    const blob = new Blob(['broken'], { type: 'application/epub+zip' })
    const { findByText } = renderWithI18n(
      <EpubViewer blob={blob} name="broken.epub" />,
      { lang: 'de' },
    )
    await findByText(/Buch konnte nicht geladen werden/i)
  })
})
