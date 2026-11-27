import { describe, it, expect, beforeAll, beforeEach, vi } from 'vitest'
import { renderWithI18n } from '../../test/renderWithI18n'
import { PreviewDialog } from './PreviewDialog'
import { apiClient } from '../../store/auth'
import type { FileSource } from './fileSource'

const rootSrc: FileSource  = { kind: 'root',  rootID: 'r-1' }
const mountSrc: FileSource = { kind: 'mount', mountID: 'm-1' }

beforeAll(() => {
  // jsdom hat kein URL.createObjectURL — Stub liefert deterministische URL.
  // @ts-expect-error jsdom-stub
  global.URL.createObjectURL = vi.fn(() => 'blob:mock')
  // @ts-expect-error jsdom-stub
  global.URL.revokeObjectURL = vi.fn()
})

beforeEach(() => {
  vi.restoreAllMocks()
  // @ts-expect-error jsdom-stub re-init
  global.URL.createObjectURL = vi.fn(() => 'blob:mock')
  // @ts-expect-error jsdom-stub re-init
  global.URL.revokeObjectURL = vi.fn()
})

function mockDownload(blob: Blob) {
  return vi.spyOn(apiClient.files, 'download').mockResolvedValue({
    ok:   true,
    blob: () => Promise.resolve(blob),
  } as any)
}

describe('PreviewDialog', () => {
  it('rendert <img> für Bilddateien', async () => {
    mockDownload(new Blob(['x'], { type: 'image/png' }))

    const { findByRole } = renderWithI18n(
      <PreviewDialog source={rootSrc} path="/foo.png" name="foo.png" onClose={() => {}} />,
      { lang: 'de' },
    )

    const img = await findByRole('img', { name: /foo\.png/i })
    expect(img).toHaveAttribute('src', 'blob:mock')
  })

  it('rendert <iframe> für PDF', async () => {
    mockDownload(new Blob(['x'], { type: 'application/pdf' }))

    const { container } = renderWithI18n(
      <PreviewDialog source={rootSrc} path="/d.pdf" name="d.pdf" onClose={() => {}} />,
      { lang: 'de' },
    )

    await vi.waitFor(() => {
      const frame = container.querySelector('iframe')
      expect(frame).not.toBeNull()
      expect(frame).toHaveAttribute('src', 'blob:mock')
    })
  })

  it('rendert <video> für Videos', async () => {
    mockDownload(new Blob(['x'], { type: 'video/mp4' }))

    const { container } = renderWithI18n(
      <PreviewDialog source={rootSrc} path="/m.mp4" name="m.mp4" onClose={() => {}} />,
      { lang: 'de' },
    )

    // Der Dialog selbst trägt schon "Vorschau: m.mp4" als Label, ist also da,
    // bevor der Blob geladen ist — auf das <video> muss eigens gewartet werden.
    await vi.waitFor(() => {
      const vid = container.querySelector('video')
      expect(vid).not.toBeNull()
      expect(vid).toHaveAttribute('src', 'blob:mock')
    })
  })

  it('rendert <audio> für Audio', async () => {
    mockDownload(new Blob(['x'], { type: 'audio/mpeg' }))

    const { container } = renderWithI18n(
      <PreviewDialog source={rootSrc} path="/s.mp3" name="s.mp3" onClose={() => {}} />,
      { lang: 'de' },
    )

    await vi.waitFor(() => {
      const aud = container.querySelector('audio')
      expect(aud).not.toBeNull()
      expect(aud).toHaveAttribute('src', 'blob:mock')
    })
  })

  it('zeigt Fehlermeldung bei Download-Fehler', async () => {
    vi.spyOn(apiClient.files, 'download').mockResolvedValue({
      ok: false, status: 500,
    } as any)

    const { findByText } = renderWithI18n(
      <PreviewDialog source={rootSrc} path="/x.png" name="x.png" onClose={() => {}} />,
      { lang: 'de' },
    )

    await findByText(/Vorschau konnte nicht geladen werden/i)
  })

  it('lädt Datei-Mounts über den Shared-Mount-Endpoint', async () => {
    const spy = vi.spyOn(apiClient.sharedMounts, 'download').mockResolvedValue({
      ok:   true,
      blob: () => Promise.resolve(new Blob(['x'], { type: 'image/png' })),
    } as any)

    const { findByRole } = renderWithI18n(
      <PreviewDialog source={mountSrc} path="/" name="cat.png" onClose={() => {}} />,
      { lang: 'de' },
    )

    await findByRole('img', { name: /cat\.png/i })
    expect(spy).toHaveBeenCalledWith('m-1', '/')
  })
})
