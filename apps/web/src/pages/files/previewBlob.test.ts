import { describe, it, expect, beforeEach, vi } from 'vitest'
import { apiClient } from '../../store/auth'
import { fetchPreviewBlob, fetchMountPreviewBlob } from './previewBlob'

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('fetchPreviewBlob', () => {
  it('liefert Server-Thumbnail-Blob für Bilder', async () => {
    const blob = new Blob(['x'], { type: 'image/png' })
    const spy = vi.spyOn(apiClient.files, 'thumbnail').mockResolvedValue({
      ok:   true,
      blob: () => Promise.resolve(blob),
    } as any)

    const out = await fetchPreviewBlob('r-1', '/a.png', 'a.png')

    expect(spy).toHaveBeenCalledWith('r-1', '/a.png', 256, 'contain')
    expect(out).toBe(blob)
  })

  it('liefert null bei nicht unterstützten Typen (Fallback auf Icon)', async () => {
    const out = await fetchPreviewBlob('r-1', '/x.pdf', 'x.pdf')
    expect(out).toBeNull()
  })

  it('liefert null, wenn Server-Thumbnail fehlschlägt', async () => {
    vi.spyOn(apiClient.files, 'thumbnail').mockResolvedValue({
      ok:     false,
      status: 415,
    } as any)
    const out = await fetchPreviewBlob('r-1', '/a.png', 'a.png')
    expect(out).toBeNull()
  })

  it('extrahiert ID3-Albumcover für Audio-Dateien', async () => {
    // Minimaler ID3v2.3-Header mit APIC-Frame: "ID3" + version + flags + size
    // + frame "APIC" + frameSize + flags + encoding + MIME + 0 + picType + desc + 0 + img
    const img  = new Uint8Array([0xFF, 0xD8, 0xFF, 0xE0]) // JPEG-Magic-Bytes
    const mime = new TextEncoder().encode('image/jpeg')
    const apicData = new Uint8Array([
      0x00,                                    // encoding latin1
      ...mime, 0x00,                            // MIME + null
      0x03,                                     // picture type = cover
      0x00,                                     // empty description + null
      ...img,
    ])
    const apicFrame = new Uint8Array(10 + apicData.length)
    apicFrame.set([0x41, 0x50, 0x49, 0x43])     // "APIC"
    // ID3v2.3 frame size = big-endian
    apicFrame[4] = (apicData.length >>> 24) & 0xff
    apicFrame[5] = (apicData.length >>> 16) & 0xff
    apicFrame[6] = (apicData.length >>> 8)  & 0xff
    apicFrame[7] =  apicData.length         & 0xff
    apicFrame[8] = 0; apicFrame[9] = 0
    apicFrame.set(apicData, 10)

    const totalSize = apicFrame.length
    const header = new Uint8Array(10)
    header.set([0x49, 0x44, 0x33])              // "ID3"
    header[3] = 0x03; header[4] = 0x00          // v2.3
    header[5] = 0x00                            // flags
    // synchsafe size
    header[6] = (totalSize >>> 21) & 0x7f
    header[7] = (totalSize >>> 14) & 0x7f
    header[8] = (totalSize >>>  7) & 0x7f
    header[9] =  totalSize         & 0x7f

    const mp3 = new Uint8Array(header.length + apicFrame.length)
    mp3.set(header, 0)
    mp3.set(apicFrame, header.length)

    vi.spyOn(apiClient.files, 'download').mockResolvedValue({
      ok:   true,
      blob: () => Promise.resolve(new Blob([mp3.buffer], { type: 'audio/mpeg' })),
    } as any)

    const out = await fetchPreviewBlob('r-1', '/song.mp3', 'song.mp3')
    expect(out).not.toBeNull()
    expect(out!.type).toContain('image/')
  })
})

describe('fetchMountPreviewBlob', () => {
  it('holt das Mount-Thumbnail für Bild-Datei-Mounts über Sub-Pfad "/"', async () => {
    const blob = new Blob(['x'], { type: 'image/png' })
    const spy = vi.spyOn(apiClient.sharedMounts, 'thumbnail').mockResolvedValue({
      ok:   true,
      blob: () => Promise.resolve(blob),
    } as any)

    const out = await fetchMountPreviewBlob('m-1', '/', 'a.png')

    expect(spy).toHaveBeenCalledWith('m-1', '/', 256, 'contain')
    expect(out).toBe(blob)
  })

  it('liefert null bei nicht unterstützten Typen', async () => {
    const out = await fetchMountPreviewBlob('m-1', '/', 'x.docx')
    expect(out).toBeNull()
  })

  it('liefert null, wenn das Mount-Thumbnail fehlschlägt', async () => {
    vi.spyOn(apiClient.sharedMounts, 'thumbnail').mockResolvedValue({
      ok:     false,
      status: 415,
    } as any)
    const out = await fetchMountPreviewBlob('m-1', '/', 'a.png')
    expect(out).toBeNull()
  })
})
