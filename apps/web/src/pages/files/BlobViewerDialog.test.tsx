import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { BlobViewerDialog } from './BlobViewerDialog'
import { apiClient } from '../../store/auth'
import type { FileSource } from './fileSource'

const rootSrc: FileSource = { kind: 'root', rootID: 'r-1' }

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('BlobViewerDialog', () => {
  it('laedt das Blob und delegiert das Rendering an die render-Prop', async () => {
    vi.spyOn(apiClient.files, 'download').mockResolvedValue({
      ok:   true,
      blob: () => Promise.resolve(new Blob(['x'], { type: 'application/epub+zip' })),
    } as any)
    const onClose = vi.fn()
    const { findByText, getByRole } = renderWithI18n(
      <BlobViewerDialog
        source={rootSrc} path="/a.epub" name="book.epub" onClose={onClose}
        render={(blob, name) => <div>rendered:{name}:{blob.type}</div>}
      />,
      { lang: 'de' },
    )
    await findByText('rendered:book.epub:application/epub+zip')
    fireEvent.click(getByRole('button'))
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('zeigt eine Fehlermeldung, wenn der Download fehlschlaegt', async () => {
    vi.spyOn(apiClient.files, 'download').mockResolvedValue({ ok: false } as any)
    const { findByText } = renderWithI18n(
      <BlobViewerDialog
        source={rootSrc} path="/a.epub" name="broken.epub" onClose={() => {}}
        render={() => <div>should not render</div>}
      />,
      { lang: 'de' },
    )
    await findByText(/Vorschau konnte nicht geladen werden/i)
  })

  it('laedt ueber den Shared-Mount-Endpoint fuer Mount-Quellen', async () => {
    const spy = vi.spyOn(apiClient.sharedMounts, 'download').mockResolvedValue({
      ok:   true,
      blob: () => Promise.resolve(new Blob(['x'])),
    } as any)
    const mountSrc: FileSource = { kind: 'mount', mountID: 'm-1' }
    renderWithI18n(
      <BlobViewerDialog
        source={mountSrc} path="/" name="x.cbz" onClose={() => {}}
        render={() => <div>ok</div>}
      />,
      { lang: 'de' },
    )
    await vi.waitFor(() => expect(spy).toHaveBeenCalledWith('m-1', '/'))
  })
})
