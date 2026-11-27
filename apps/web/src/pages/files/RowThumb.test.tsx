import { describe, it, expect, vi, beforeEach } from 'vitest'
import { waitFor } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { apiClient } from '../../store/auth'
import { RowThumb } from './RowThumb'
import { thumbHostFor, type DirEntry } from './helpers'
import type { SharedMount } from '@privasync/api-client'

beforeEach(() => {
  vi.restoreAllMocks()
  // Vitest 4 patches URL.createObjectURL via jsdom Blob internals (_buffer),
  // which jsdom 30 no longer exposes. The component only needs a string back.
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:thumb-mock')
})

function fileMount(over: Partial<SharedMount> = {}): SharedMount {
  return {
    id: 'm-1', name: 'cat.jpg', path: '/Photos/cat.jpg', is_file: true,
    can_write: false, expires_at: null,
    owner: { id: 'u-a', username: 'alice', avatar_url: null },
    ...over,
  }
}

describe('RowThumb für Einzeldatei-Mounts', () => {
  it('lädt das Icon-Thumbnail über den Mount-Endpoint mit Sub-Pfad "/"', async () => {
    const blob = new Blob(['x'], { type: 'image/jpeg' })
    const mountSpy = vi.spyOn(apiClient.sharedMounts, 'thumbnail').mockResolvedValue({
      ok:   true,
      blob: () => Promise.resolve(blob),
    } as any)
    const rootSpy = vi.spyOn(apiClient.files, 'thumbnail')

    const entry: DirEntry = { kind: 'file', name: 'cat.jpg', mount: fileMount(), file: {
      id: 'mount:m-1', path: '/Photos/cat.jpg', size: 0, mtime: '',
      content_hash: '', is_deleted: false, updated_at: '',
    } }
    // selectedRoot is undefined in the root list — the mount decoration alone
    // must drive the dispatch.
    const host = thumbHostFor(entry, undefined)
    expect(host).not.toBeNull()

    renderWithI18n(<RowThumb {...host!} name={entry.name} />)

    await waitFor(() =>
      expect(mountSpy).toHaveBeenCalledWith('m-1', '/', 64),
    )
    expect(rootSpy).not.toHaveBeenCalled()
  })

  it('Grid (lg) Datei IM Mount-Verzeichnis: Vorschau über mount-relativen Sub-Pfad', async () => {
    // A file browsed inside a dir-mount has its own sub-path; the grid preview
    // must request that path, not the mount root "/", or every tile shows the
    // wrong (or no) image.
    const blob = new Blob(['x'], { type: 'image/jpeg' })
    const mountThumb = vi.spyOn(apiClient.sharedMounts, 'thumbnail').mockResolvedValue({
      ok:   true,
      blob: () => Promise.resolve(blob),
    } as any)

    renderWithI18n(
      <RowThumb rootID="mount:s-1" path="/Reports/cat.jpg" mountID="s-1" name="cat.jpg" size="lg" />,
    )

    await waitFor(() =>
      expect(mountThumb).toHaveBeenCalledWith('s-1', '/Reports/cat.jpg', 256, 'contain'),
    )
  })

  it('zieht im Grid (lg) Container-Cover über die Mount-Vorschau (Parität mit eigenen Dateien)', async () => {
    // Audio has no server thumbnail → the cheap thumb path gives up. Full
    // parity requires the client-extraction path (download + ID3 cover), the
    // same one owned grid files take via fetchPreviewBlob.
    vi.spyOn(apiClient.sharedMounts, 'thumbnail').mockResolvedValue({
      ok:     false,
      status: 415,
    } as any)
    const mountDownload = vi.spyOn(apiClient.sharedMounts, 'download').mockResolvedValue({
      ok:   true,
      blob: () => Promise.resolve(new Blob(['no-cover'], { type: 'audio/mpeg' })),
    } as any)

    const entry: DirEntry = { kind: 'file', name: 'song.mp3', mount: fileMount({ name: 'song.mp3', path: '/Music/song.mp3' }), file: {
      id: 'mount:m-1', path: '/Music/song.mp3', size: 0, mtime: '',
      content_hash: '', is_deleted: false, updated_at: '',
    } }
    const host = thumbHostFor(entry, undefined)

    renderWithI18n(<RowThumb {...host!} name={entry.name} size="lg" />)

    // Grid parity: the client-extraction path must be taken for audio mounts,
    // i.e. the mount file is downloaded for cover extraction (sub-path "/").
    await waitFor(() =>
      expect(mountDownload).toHaveBeenCalledWith('m-1', '/'),
    )
  })
})
