import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderWithI18n } from '../../test/renderWithI18n'
import { AudioPlayer } from './AudioPlayer'
import { apiClient } from '../../store/auth'
import { useAudioPlayer } from '../../store/audioPlayer'
import type { FileSource } from './fileSource'

const rootSrc: FileSource = { kind: 'root', rootID: 'r-1' }

beforeEach(() => {
  vi.restoreAllMocks()
  // @ts-expect-error jsdom-stub
  global.URL.createObjectURL = vi.fn(() => 'blob:cover-mock')
  // @ts-expect-error jsdom-stub
  global.URL.revokeObjectURL = vi.fn()
})

afterEach(() => {
  useAudioPlayer.getState().close()
})

function mockMediaStreamUrl() {
  return vi.spyOn(apiClient.files, 'mediaStreamUrl').mockResolvedValue({
    url: 'https://api.test/media/tok-1', expiresIn: 900,
  })
}

function mockRangeDownload() {
  return vi.spyOn(apiClient.files, 'download').mockResolvedValue({
    ok: true, blob: () => Promise.resolve(new Blob(['x'], { type: 'audio/mpeg' })),
  } as any)
}

describe('AudioPlayer — Streaming (ADR-0067)', () => {
  it('rendert das <audio>-Element mit der Media-Stream-URL statt einem Voll-Download', async () => {
    const mint = mockMediaStreamUrl()
    const download = mockRangeDownload()
    useAudioPlayer.getState().play(rootSrc, [{ path: '/song-a.mp3', name: 'song-a.mp3' }], 0)

    const { container } = renderWithI18n(<AudioPlayer />, { lang: 'de' })

    await vi.waitFor(() => {
      const audio = container.querySelector('audio')
      expect(audio).not.toBeNull()
      expect(audio).toHaveAttribute('src', 'https://api.test/media/tok-1')
    })
    expect(mint).toHaveBeenCalledWith('r-1', '/song-a.mp3')
    // Playback never triggers a full download of the track.
    expect(download).not.toHaveBeenCalledWith('r-1', '/song-a.mp3')
  })

  it('lädt Cover-Art nur über einen Range-Fetch der ersten 1 MB, nicht die ganze Datei', async () => {
    mockMediaStreamUrl()
    const download = mockRangeDownload()
    useAudioPlayer.getState().play(rootSrc, [{ path: '/song-a.mp3', name: 'song-a.mp3' }], 0)

    renderWithI18n(<AudioPlayer />, { lang: 'de' })

    await vi.waitFor(() => expect(download).toHaveBeenCalled())
    expect(download).toHaveBeenCalledWith('r-1', '/song-a.mp3', { range: 'bytes=0-1048575' })
  })

  it('zeigt eine Fehlermeldung, wenn das Minten des Streaming-Grants fehlschlägt', async () => {
    vi.spyOn(apiClient.files, 'mediaStreamUrl').mockRejectedValue(new Error('boom'))
    mockRangeDownload()
    useAudioPlayer.getState().play(rootSrc, [{ path: '/song-a.mp3', name: 'song-a.mp3' }], 0)

    const { findByText } = renderWithI18n(<AudioPlayer />, { lang: 'de' })

    await findByText('Titel konnte nicht geladen werden.')
  })
})
