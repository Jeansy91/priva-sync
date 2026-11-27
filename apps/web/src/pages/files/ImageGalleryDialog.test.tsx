import { describe, it, expect, beforeAll, beforeEach, vi } from 'vitest'
import { fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { ImageGalleryDialog } from './ImageGalleryDialog'
import { apiClient } from '../../store/auth'
import type { FileSource } from './fileSource'

const rootSrc: FileSource = { kind: 'root', rootID: 'r-1' }

beforeAll(() => {
  // @ts-expect-error jsdom-stub
  global.URL.createObjectURL = vi.fn(() => 'blob:mock')
  // @ts-expect-error jsdom-stub
  global.URL.revokeObjectURL = vi.fn()
})

beforeEach(() => {
  vi.restoreAllMocks()
  // @ts-expect-error jsdom-stub
  global.URL.createObjectURL = vi.fn(() => 'blob:mock')
  // @ts-expect-error jsdom-stub
  global.URL.revokeObjectURL = vi.fn()
})

const images = [
  { path: '/a.png', name: 'a.png' },
  { path: '/b.jpg', name: 'b.jpg' },
  { path: '/c.svg', name: 'c.svg' },
]

function mockDownload() {
  return vi.spyOn(apiClient.files, 'download').mockResolvedValue({
    ok: true, blob: () => Promise.resolve(new Blob(['x'], { type: 'image/png' })),
  } as any)
}

function mockThumbnail() {
  return vi.spyOn(apiClient.files, 'thumbnail').mockResolvedValue({
    ok: true, blob: () => Promise.resolve(new Blob(['t'], { type: 'image/jpeg' })),
  } as any)
}

describe('ImageGalleryDialog — Rondell Video-Fallback', () => {
  it('lädt NICHT die volle Video-Datei wenn Server-Thumb fehlt', async () => {
    // IO immediately triggert
    class IO {
      cb: (entries: any[]) => void
      constructor(cb: (entries: any[]) => void) { this.cb = cb }
      observe(el: Element) { this.cb([{ isIntersecting: true, target: el }]) }
      unobserve() {} disconnect() {}
    }
    ;(globalThis as any).IntersectionObserver = IO as any

    const dl = vi.spyOn(apiClient.files, 'download').mockResolvedValue({
      ok: true, blob: () => Promise.resolve(new Blob(['v'], { type: 'video/mp4' })),
    } as any)
    vi.spyOn(apiClient.files, 'thumbnail').mockResolvedValue({
      ok: false, status: 415,
    } as any)

    const media = [
      { path: '/a.png', name: 'a.png' },
      { path: '/c.mp4', name: 'c.mp4' },
    ]
    renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={media} startIndex={0} onClose={() => {}} />,
      { lang: 'de' },
    )
    // Warten bis das Hauptbild geladen ist — erst dann sagt die Abwesenheit
    // eines c.mp4-Downloads etwas aus.
    await vi.waitFor(() => expect(dl).toHaveBeenCalledWith('r-1', '/a.png'))

    // Hauptbild = 1 download (a.png). Kein download von c.mp4 für Thumb.
    const paths = dl.mock.calls.map((c) => c[1])
    expect(paths).not.toContain('/c.mp4')
  })
})

describe('ImageGalleryDialog — Video-Support', () => {
  it('rendert ein <video> Element mit Media-Stream-URL statt Voll-Download (ADR-0067)', async () => {
    mockDownload()
    const mintSpy = vi.spyOn(apiClient.files, 'mediaStreamUrl').mockResolvedValue({
      url: 'https://api.test/media/tok-1', expiresIn: 900,
    })
    const media = [
      { path: '/a.png', name: 'a.png' },
      { path: '/clip.mp4', name: 'clip.mp4' },
    ]
    const { container } = renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={media} startIndex={1} onClose={() => {}} />,
      { lang: 'de' },
    )
    await vi.waitFor(() => {
      const v = container.querySelector('video')
      expect(v).not.toBeNull()
      expect(v).toHaveAttribute('src', 'https://api.test/media/tok-1')
    })
    expect(mintSpy).toHaveBeenCalledWith('r-1', '/clip.mp4')
    // The full file is never downloaded for playback — only the grant is minted.
    expect(apiClient.files.download).not.toHaveBeenCalledWith('r-1', '/clip.mp4')
  })
})

describe('ImageGalleryDialog', () => {
  it('zeigt das Start-Bild', async () => {
    mockDownload()
    const { findByRole } = renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={images} startIndex={1} onClose={() => {}} />,
      { lang: 'de' },
    )
    const img = await findByRole('img', { name: /b\.jpg/i })
    expect(img).toHaveAttribute('src', 'blob:mock')
  })

  it('Weiter-Button lädt nächstes Bild', async () => {
    const dl = mockDownload()
    const { findByRole, findByLabelText } = renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={images} startIndex={0} onClose={() => {}} />,
      { lang: 'de' },
    )
    await findByRole('img', { name: /a\.png/i })
    const next = await findByLabelText(/n.chstes Bild/i)
    fireEvent.click(next)
    await findByRole('img', { name: /b\.jpg/i })
    expect(dl).toHaveBeenCalledWith('r-1', '/b.jpg')
  })

  it('Zurück-Button lädt voriges Bild', async () => {
    mockDownload()
    const { findByRole, findByLabelText } = renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={images} startIndex={2} onClose={() => {}} />,
      { lang: 'de' },
    )
    await findByRole('img', { name: /c\.svg/i })
    fireEvent.click(await findByLabelText(/voriges Bild/i))
    await findByRole('img', { name: /b\.jpg/i })
  })

  it('Pfeil-Tasten navigieren', async () => {
    mockDownload()
    const { findByRole } = renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={images} startIndex={0} onClose={() => {}} />,
      { lang: 'de' },
    )
    await findByRole('img', { name: /a\.png/i })
    fireEvent.keyDown(window, { key: 'ArrowRight' })
    await findByRole('img', { name: /b\.jpg/i })
    fireEvent.keyDown(window, { key: 'ArrowLeft' })
    await findByRole('img', { name: /a\.png/i })
  })

  it('Counter zeigt Position', async () => {
    mockDownload()
    const { findByText } = renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={images} startIndex={1} onClose={() => {}} />,
      { lang: 'de' },
    )
    await findByText('2 / 3')
  })

  it('zeigt Thumbnail-Rondell mit allen Bildern und springt per Klick', async () => {
    mockDownload()
    const { findByRole, findAllByLabelText } = renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={images} startIndex={0} onClose={() => {}} />,
      { lang: 'de' },
    )
    await findByRole('img', { name: /a\.png/i })

    const thumbs = await findAllByLabelText(/Zu Bild/i)
    expect(thumbs).toHaveLength(3)

    fireEvent.click(thumbs[2])
    await findByRole('img', { name: /c\.svg/i })
  })

  it('Aktives Thumbnail ist als aktuell markiert', async () => {
    mockDownload()
    const { findAllByLabelText } = renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={images} startIndex={1} onClose={() => {}} />,
      { lang: 'de' },
    )
    const thumbs = await findAllByLabelText(/Zu Bild/i)
    expect(thumbs[1]).toHaveAttribute('aria-current', 'true')
    expect(thumbs[0]).not.toHaveAttribute('aria-current', 'true')
  })

  it('Thumbnails nutzen den Thumbnail-Endpoint, nicht den Volldownload', async () => {
    mockDownload()
    const thumb = mockThumbnail()
    // IO immediately triggert
    class IO {
      cb: (entries: any[]) => void
      constructor(cb: (entries: any[]) => void) { this.cb = cb }
      observe(el: Element) { this.cb([{ isIntersecting: true, target: el }]) }
      unobserve() {} disconnect() {}
    }
    ;(globalThis as any).IntersectionObserver = IO as any

    renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={images} startIndex={0} onClose={() => {}} />,
      { lang: 'de' },
    )
    await vi.waitFor(() => expect(thumb).toHaveBeenCalled())
    expect(thumb.mock.calls[0][0]).toBe('r-1')
  })

  it('lädt Thumbnails erst wenn sichtbar (IntersectionObserver)', async () => {
    const dl = mockDownload()
    const thumb = mockThumbnail()

    // Stub IntersectionObserver — keine Observation triggert -> kein Thumbnail-Download.
    const observers: { cb: (entries: any[]) => void; targets: Element[] }[] = []
    class IO {
      cb: (entries: any[]) => void
      targets: Element[] = []
      constructor(cb: (entries: any[]) => void) {
        this.cb = cb
        observers.push(this)
      }
      observe(el: Element) { this.targets.push(el) }
      unobserve() {}
      disconnect() {}
    }
    ;(globalThis as any).IntersectionObserver = IO as any

    const big = Array.from({ length: 20 }, (_, i) => ({
      path: `/p${i}.png`, name: `p${i}.png`,
    }))

    renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={big} startIndex={0} onClose={() => {}} />,
      { lang: 'de' },
    )

    // Warte bis Render fertig: Hauptbild lädt (1 download für p0.png), und
    // jeder Thumbnail hat seinen Observer registriert.
    await vi.waitFor(() => expect(dl).toHaveBeenCalledTimes(1))
    expect(dl).toHaveBeenCalledWith('r-1', '/p0.png')
    await vi.waitFor(() => expect(observers.length).toBeGreaterThanOrEqual(5))

    // Jeder Thumbnail hat seinen eigenen Observer — triggere die ersten 5.
    for (let i = 0; i < 5; i++) {
      const o = observers[i]
      o.cb(o.targets.map((t) => ({ isIntersecting: true, target: t })))
    }

    // Hauptbild = download, Thumbs = thumbnail-Endpoint.
    await vi.waitFor(() => expect(thumb).toHaveBeenCalledTimes(5))
    expect(dl).toHaveBeenCalledTimes(1)
  })

  it('Rondell ist eigene Zeile, überlagert das Bild nicht', async () => {
    mockDownload()
    const { findByRole, container } = renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={images} startIndex={0} onClose={() => {}} />,
      { lang: 'de' },
    )
    const img = await findByRole('img', { name: /a\.png/i })
    const strip = container.querySelector('[data-testid="gallery-strip"]')
    expect(strip).not.toBeNull()
    // Strip darf das Bild nicht enthalten (nicht überlagernd).
    expect(strip!.contains(img)).toBe(false)
    // Strip darf nicht absolute positioniert sein (kein overlay über Bild-Bereich).
    expect(strip!.className).not.toMatch(/\babsolute\b/)
  })

  it('an Rändern sind die Buttons deaktiviert', async () => {
    mockDownload()
    const { findByLabelText, rerender } = renderWithI18n(
      <ImageGalleryDialog source={rootSrc} images={images} startIndex={0} onClose={() => {}} />,
      { lang: 'de' },
    )
    expect(await findByLabelText(/voriges Bild/i)).toBeDisabled()

    rerender(
      <ImageGalleryDialog source={rootSrc} images={images} startIndex={2} onClose={() => {}} />,
    )
    expect(await findByLabelText(/n.chstes Bild/i)).toBeDisabled()
  })
})
