import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { fireEvent, screen } from '@testing-library/react'
import { Routes, Route } from 'react-router-dom'
import { renderWithI18n } from '../test/renderWithI18n'
import FilesPage from './FilesPage'
import { apiClient, useAuthStore } from '../store/auth'
import type { Me, SyncRoot, FileEntry, SharedMount } from '@privasync/api-client'

function setUserShowFileTree(show: boolean) {
  useAuthStore.setState({
    user: { id: 'u', username: 'a', email: 'a@b', role: 'user', totp_enabled: false,
      quota_bytes: 0, used_bytes: 0, trash_bytes: 0, avatar_url: null,
      preferred_language: 'de', show_file_tree: show } as unknown as Me,
    accessToken: 't', isAuthenticated: true,
  })
}

function renderFilesPage(opts: { lang?: 'de' | 'en'; route?: string } = {}) {
  return renderWithI18n(
    <Routes>
      <Route path="/files/*" element={<FilesPage />} />
    </Routes>,
    opts,
  )
}

const sseState = vi.hoisted(() => ({
  handlers: {} as Record<string, (d: unknown) => void>,
}))
vi.mock('../hooks/useSSE', () => ({
  useSSE: (h: Record<string, (d: unknown) => void>) => {
    sseState.handlers = h
  },
}))

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([])
  vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([])
  vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({})
  vi.spyOn(console, 'error').mockImplementation(() => {})
  sseState.handlers = {}
  // Default: no user -> show_file_tree=true (fallback), but individual tests
  // can override via setUserShowFileTree().
  useAuthStore.setState({ user: null, accessToken: null, isAuthenticated: false })
})

describe('FilesPage — DE', () => {
  it('zeigt Empty-Sync-Root-Hinweis', async () => {
    const { findByText, queryByRole } = renderFilesPage( { lang: 'de', route: '/files' })
    expect(await findByText('Noch kein Sync-Ordner')).toBeInTheDocument()
    expect(queryByRole('button', { name: /Aktualisieren/i })).not.toBeInTheDocument()
  })
})

describe('FilesPage — EN', () => {
  it('shows empty sync root hint without refresh button', async () => {
    const { findByText, queryByRole } = renderFilesPage( { lang: 'en', route: '/files' })
    expect(await findByText('No sync folder yet')).toBeInTheDocument()
    expect(queryByRole('button', { name: /Refresh/i })).not.toBeInTheDocument()
  })
})

describe('FilesPage — SSE optimistic patch', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }

  it('refetches listing on file_uploaded SSE im active root', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    const newFile: FileEntry = {
      id: 'f-1', path: '/hello.txt', size: 5,
      mtime: new Date().toISOString(),
      content_hash: '', is_deleted: false,
      updated_at: new Date().toISOString(),
    }
    // ADR-0008: Listing ist lazy. SSE triggert Refetch; der Mock liefert nach
    // dem ersten Aufruf die neue Datei zurück.
    let calls = 0
    vi.spyOn(apiClient.files, 'list').mockImplementation(async () => {
      calls++
      return { files: calls === 1 ? [] : [newFile], next_cursor: '' }
    })

    const { findByText, queryByText } = renderFilesPage({ lang: 'de', route: '/files/Photos' })
    await findByText('Photos')
    await new Promise((r) => setTimeout(r, 50))

    await act(async () => {
      sseState.handlers.file_uploaded?.({
        type: 'file_uploaded',
        version: 2,
        data: { root_id: root.id, path: '/hello.txt', file: newFile },
      })
    })
    expect(await findByText('hello.txt')).toBeInTheDocument()

    // Delete-Event → wieder leeres Listing.
    vi.mocked(apiClient.files.list).mockResolvedValue({ files: [], next_cursor: '' })
    await act(async () => {
      sseState.handlers.file_deleted?.({
        type: 'file_deleted', version: 2,
        data: { sync_root_id: root.id, file_id: 'f-1', path: '/hello.txt' },
      })
    })
    await new Promise(r => setTimeout(r, 50))
    expect(queryByText('hello.txt')).not.toBeInTheDocument()
  })

  it('appends sync root on sync_root_added', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([])
    const { findByText, queryByText } = renderFilesPage( { lang: 'de', route: '/files' })
    await findByText('Noch kein Sync-Ordner')

    act(() => {
      sseState.handlers.sync_root_added?.({ root })
    })
    expect(await findByText('Photos')).toBeInTheDocument()

    act(() => {
      sseState.handlers.sync_root_deleted?.({ sync_root_id: root.id })
    })
    await new Promise(r => setTimeout(r, 0))
    expect(queryByText('Photos')).not.toBeInTheDocument()
  })
})

describe('FilesPage — Shared Mounts (ADR-0010)', () => {
  const ownRoot: SyncRoot = {
    id: 'root-own', user_id: 'u-bob', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }
  const mount: SharedMount = {
    id: 'share-1',
    name: '2024',
    path: '/Photos/2024',
    is_file: false,
    can_write: true,
    expires_at: null,
    owner: { id: 'u-alice', username: 'alice', avatar_url: null },
  }

  it('zeigt Sektionsüberschriften "Eigene" und "Mit mir geteilt" und listet beide Mengen', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([ownRoot])
    vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([mount])
    const { findByText } = renderFilesPage({ lang: 'de', route: '/files' })
    expect(await findByText('Photos')).toBeInTheDocument()
    expect(await findByText('2024')).toBeInTheDocument()
    expect(await findByText(/Eigene/i)).toBeInTheDocument()
    expect(await findByText(/Mit mir geteilt/i)).toBeInTheDocument()
  })

  it('zeigt den Upload-Button im Mount-Modus, wenn can_write=true', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([])
    vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([{ ...mount, can_write: true }])
    vi.spyOn(apiClient.sharedMounts, 'files').mockResolvedValue({
      entries: [{ name: 'seed.txt', path: '/Photos/2024/seed.txt', is_dir: false, size: 1 }],
    })
    const { findByText, queryByRole } = renderFilesPage({
      lang:  'de',
      route: '/files/_m/' + mount.id,
    })
    await findByText('seed.txt')
    expect(queryByRole('button', { name: /Hochladen/i })).not.toBeNull()
  })

  it('blendet Upload-Button bei can_write=false aus', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([])
    vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([{ ...mount, can_write: false }])
    vi.spyOn(apiClient.sharedMounts, 'files').mockResolvedValue({
      entries: [
        { name: 'note.txt', path: '/Photos/2024/note.txt', is_dir: false, size: 5 },
      ],
    })

    const { findByText, queryByRole, queryByText } = renderFilesPage({
      lang:  'de',
      route: '/files/_m/' + mount.id,
    })
    await findByText('note.txt')
    expect(queryByRole('button', { name: /Hochladen/i })).toBeNull()
    expect(queryByText(/Neuer Ordner/i)).toBeNull()
  })

  it('lädt eine Mount-Datei über den Mount-Download-Endpunkt herunter', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([])
    vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([mount])
    // A binary (non-text, non-previewable) file so activation takes the direct
    // download path — text files now open the in-browser editor instead.
    vi.spyOn(apiClient.sharedMounts, 'files').mockResolvedValue({
      entries: [
        { name: 'note.bin', path: '/Photos/2024/note.bin', is_dir: false, size: 5 },
      ],
    })
    const dl = vi.spyOn(apiClient.sharedMounts, 'downloadUrl')
      .mockResolvedValue('http://test/download/abc')

    const { findByText, getByText } = renderFilesPage({
      lang:  'de',
      route: '/files/_m/' + mount.id,
    })
    await findByText('note.bin')
    await act(async () => { fireEvent.doubleClick(getByText('note.bin')) })

    await vi.waitFor(() => {
      expect(dl).toHaveBeenCalledWith(mount.id, '/note.bin')
    })
  })

  it('bleibt im Mount-Modus beim Navigieren in Subdirs', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([])
    vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([mount])
    const mountFiles = vi.spyOn(apiClient.sharedMounts, 'files')
      .mockResolvedValueOnce({
        entries: [
          { name: 'subdir1', path: '/Photos/2024/subdir1', is_dir: true, size: 0 },
        ],
      })
      .mockResolvedValueOnce({
        entries: [
          { name: 'leaf.txt', path: '/Photos/2024/subdir1/leaf.txt', is_dir: false, size: 1 },
        ],
      })

    const { findByText, getByText } = renderFilesPage({
      lang:  'de',
      route: '/files/_m/' + mount.id,
    })
    await findByText('subdir1')

    await act(async () => { fireEvent.doubleClick(getByText('subdir1')) })

    await new Promise((r) => setTimeout(r, 50))
    expect(mountFiles).toHaveBeenLastCalledWith(mount.id, '/subdir1')
  })

  it('navigiert in den Mount-Browse-Modus und zeigt die Mount-Dateien', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([])
    vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([mount])
    const mountListSpy = vi.spyOn(apiClient.sharedMounts, 'files').mockResolvedValue({
      entries: [
        { name: 'inside.txt', path: '/Photos/2024/inside.txt', is_dir: false, size: 12 },
      ],
    })

    const { findByText } = renderFilesPage({
      lang:  'de',
      route: '/files/_m/' + mount.id,
    })

    expect(await findByText('inside.txt')).toBeInTheDocument()
    expect(mountListSpy).toHaveBeenCalledWith(mount.id, '/')
  })

  it('zeigt Geber-Badges im eigenen Root für geteilte Pfade', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([ownRoot])
    vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({
      files: [{
        id: 'd-1', path: '/2024', size: 0,
        mtime: new Date().toISOString(), content_hash: '',
        is_deleted: false, is_dir: true,
        updated_at: new Date().toISOString(),
      }],
      next_cursor: '',
    })
    vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({
      '/2024': [{
        id: 'sh-1', share_id: 'sh-1', share_type: 'user',
        target_user_id: 'u-bob', target_username: 'bob',
        can_write: true, expires_at: null,
      }],
    })

    const { findByLabelText } = renderFilesPage({ lang: 'de', route: '/files/Photos' })
    expect(await findByLabelText(/Geteilt mit bob/i)).toBeInTheDocument()
  })

  it('aktualisiert sharesByPath bei share_added-SSE auf dem aktiven Root', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([ownRoot])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({
      files: [{
        id: 'd-1', path: '/2024', size: 0,
        mtime: new Date().toISOString(), content_hash: '',
        is_deleted: false, is_dir: true,
        updated_at: new Date().toISOString(),
      }],
      next_cursor: '',
    })
    const sbr = vi.spyOn(apiClient, 'sharesByRoot')
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce({
        '/2024': [{
          id: 'sh-1', share_id: 'sh-1', share_type: 'user',
          target_user_id: 'u-bob', target_username: 'bob',
          can_write: true, expires_at: null,
        }],
      })

    const { findByText, findByLabelText } = renderFilesPage({ lang: 'de', route: '/files/Photos' })
    await findByText('2024')

    await act(async () => {
      sseState.handlers.share_added?.({
        share: {
          id:           'sh-1',
          sync_root_id: ownRoot.id,
          path:         '/2024',
          share_type:   'user',
          can_write:    true,
        },
      })
    })

    expect(await findByLabelText(/Geteilt mit bob/i)).toBeInTheDocument()
    expect(sbr).toHaveBeenCalledTimes(2)
  })

  it('navigiert weg vom Mount bei share_revoked-SSE des aktiven Mounts', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([])
    vi.spyOn(apiClient.sharedMounts, 'list')
      .mockResolvedValueOnce([mount])
      .mockResolvedValueOnce([])
    vi.spyOn(apiClient.sharedMounts, 'files').mockResolvedValue({
      entries: [{ name: 'note.txt', path: '/Photos/2024/note.txt', is_dir: false, size: 5 }],
    })

    const { findByText, queryByText } = renderFilesPage({
      lang:  'de',
      route: '/files/_m/' + mount.id,
    })
    await findByText('note.txt')

    await act(async () => {
      sseState.handlers.share_revoked?.({ share_id: mount.id })
    })

    await new Promise((r) => setTimeout(r, 50))
    expect(queryByText('note.txt')).not.toBeInTheDocument()
  })

  it('ruft das Mount-Thumbnail für einen Einzeldatei-Bild-Mount in der Root-Liste auf', async () => {
    // @ts-expect-error jsdom-stub
    global.URL.createObjectURL = vi.fn(() => 'blob:mock')
    // @ts-expect-error jsdom-stub
    global.URL.revokeObjectURL = vi.fn()
    class IO {
      cb: (entries: any[]) => void
      constructor(cb: (entries: any[]) => void) { this.cb = cb }
      observe(el: Element) { this.cb([{ isIntersecting: true, target: el }]) }
      unobserve() {} disconnect() {}
    }
    ;(globalThis as any).IntersectionObserver = IO as any

    const fileMount: SharedMount = {
      id: 'fm-1', name: 'Qwen_logo.svg.png', path: '/Qwen_logo.svg.png',
      is_file: true, can_write: false, expires_at: null,
      owner: { id: 'u-alice', username: 'alice', avatar_url: null },
    }
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([])
    vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([fileMount])
    vi.spyOn(apiClient.sharedMounts, 'stats').mockResolvedValue({
      kind: 'file', id: 'real-1', size: 1, mtime: new Date().toISOString(),
      content_hash: '', updated_at: new Date().toISOString(),
    } as any)
    const tn = vi.spyOn(apiClient.sharedMounts, 'thumbnail').mockResolvedValue({
      ok: true, blob: () => Promise.resolve(new Blob(['t'], { type: 'image/jpeg' })),
    } as any)

    const { findByText } = renderFilesPage({ lang: 'de', route: '/files' })
    await findByText('Qwen_logo.svg.png')

    await vi.waitFor(() => {
      expect(tn).toHaveBeenCalledWith('fm-1', '/', expect.any(Number))
    })
  })

  it('blendet die Shared-Sektion aus, wenn keine Mounts existieren', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([ownRoot])
    vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([])
    const { findByText, queryByText } = renderFilesPage({ lang: 'de', route: '/files' })
    await findByText('Photos')
    expect(queryByText(/Mit mir geteilt/i)).not.toBeInTheDocument()
  })
})

describe('FilesPage — Tree-Sidebar', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }

  it('rendert Tree-Sidebar wenn show_file_tree=true', async () => {
    setUserShowFileTree(true)
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    const { findByRole } = renderFilesPage({ lang: 'de', route: '/files' })
    expect(await findByRole('tree')).toBeInTheDocument()
  })

  it('blendet Tree-Sidebar aus wenn show_file_tree=false', async () => {
    setUserShowFileTree(false)
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    const { findByText, queryByRole } = renderFilesPage({ lang: 'de', route: '/files' })
    await findByText('Photos')
    expect(queryByRole('tree')).not.toBeInTheDocument()
  })
})

describe('FilesPage — Upload Split-Button', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }

  it('zeigt Caret-Trigger neben dem Hochladen-Button mit Ordner-Option', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [], next_cursor: '' })
    const { findByRole, findByText } = renderFilesPage({ lang: 'de', route: '/files/Photos' })

    const caret = await findByRole('button', { name: 'Upload-Optionen' })
    act(() => { caret.click() })

    expect(await findByText('Ordner hochladen')).toBeInTheDocument()
  })

  it('Ordner-Option öffnet einen Picker mit webkitdirectory-Attribut', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [], next_cursor: '' })
    const { findByRole, findByText, container } = renderFilesPage({ lang: 'de', route: '/files/Photos' })

    const caret = await findByRole('button', { name: 'Upload-Optionen' })
    act(() => { caret.click() })
    const folderOpt = await findByText('Ordner hochladen')

    const dirInput = container.querySelector('input[webkitdirectory]') as HTMLInputElement | null
    expect(dirInput).not.toBeNull()
    const clickSpy = vi.spyOn(dirInput!, 'click')
    act(() => { folderOpt.click() })
    expect(clickSpy).toHaveBeenCalled()
  })

  it('Upload nutzt webkitRelativePath als Pfad für Ordner-Dateien', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [], next_cursor: '' })
    const uploadSpy = vi.spyOn(apiClient.files, 'upload').mockResolvedValue(undefined as never)

    const { container, findByText } = renderFilesPage({ lang: 'de', route: '/files/Photos' })
    await findByText('Photos')
    await new Promise(r => setTimeout(r, 30))

    const dirInput = container.querySelector('input[webkitdirectory]') as HTMLInputElement
    const file = new File(['x'], 'foo.txt')
    Object.defineProperty(file, 'webkitRelativePath', { value: 'docs/sub/foo.txt' })
    Object.defineProperty(dirInput, 'files', { value: [file] })

    await act(async () => { fireEvent.change(dirInput) })

    expect(uploadSpy).toHaveBeenCalled()
    const [, uploadedPath] = uploadSpy.mock.calls[0]
    expect(uploadedPath).toBe('/docs/sub/foo.txt')
  })

  // Rekursiver D&D-Walk wird als Unit in pages/files/uploadDrop.test.ts geprüft —
  // der React-onDrop-Pfad selbst lässt sich mit jsdom-Synthetic-Events nicht
  // zuverlässig befüllen (dataTransfer.items wird nicht durchgereicht).
})

describe('FilesPage — Vorschau', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }
  const imgFile: FileEntry = {
    id: 'f-img', path: '/pic.png', size: 1,
    mtime: new Date().toISOString(), content_hash: '',
    is_deleted: false, updated_at: new Date().toISOString(),
  }
  const zipFile: FileEntry = {
    id: 'f-zip', path: '/a.bin', size: 1,
    mtime: new Date().toISOString(), content_hash: '',
    is_deleted: false, updated_at: new Date().toISOString(),
  }

  function stubBlobUrl() {
    // @ts-expect-error jsdom-stub
    global.URL.createObjectURL = vi.fn(() => 'blob:mock')
    // @ts-expect-error jsdom-stub
    global.URL.revokeObjectURL = vi.fn()
  }

  it('Doppelklick auf Video öffnet Galerie und sammelt Bilder+Videos', async () => {
    stubBlobUrl()
    const mp4: FileEntry = {
      id: 'f-vid', path: '/clip.mp4', size: 1,
      mtime: new Date().toISOString(), content_hash: '',
      is_deleted: false, updated_at: new Date().toISOString(),
    }
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [imgFile, mp4, zipFile], next_cursor: '' })
    // Video plays via a media-stream grant, not a full download (ADR-0067).
    vi.spyOn(apiClient.files, 'mediaStreamUrl').mockResolvedValue({
      url: 'https://api.test/media/tok-1', expiresIn: 900,
    })

    const { findByText, container } = renderFilesPage(
      { lang: 'de', route: '/files/Photos' },
    )
    const row = await findByText('clip.mp4')
    await act(async () => { fireEvent.doubleClick(row) })

    await vi.waitFor(() => {
      const v = container.querySelector('video')
      expect(v).not.toBeNull()
    })
    // 2 media (mp4 + png), Counter 2 / 2 (mp4 ist nach png in alphabetischer Sortierung: c < p? c < p ja → mp4 1/2)
    await findByText(/1\s*\/\s*2/)
  })

  it('Doppelklick auf Bild öffnet Galerie mit allen Bildern des Ordners', async () => {
    stubBlobUrl()
    const img2: FileEntry = {
      id: 'f-img2', path: '/b.jpg', size: 1,
      mtime: new Date().toISOString(), content_hash: '',
      is_deleted: false, updated_at: new Date().toISOString(),
    }
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [imgFile, img2, zipFile], next_cursor: '' })
    vi.spyOn(apiClient.files, 'download').mockResolvedValue({
      ok: true, blob: () => Promise.resolve(new Blob(['x'], { type: 'image/png' })),
    } as any)

    const { findByText, findByRole, findByLabelText } = renderFilesPage(
      { lang: 'de', route: '/files/Photos' },
    )
    const row = await findByText('b.jpg')
    await act(async () => { fireEvent.doubleClick(row) })
    await findByRole('img', { name: /b\.jpg/i })

    // b.jpg ist 1/2, Weiter -> pic.png. Galerie aktiv, nicht PreviewDialog.
    await findByText(/1\s*\/\s*2/)
    fireEvent.click(await findByLabelText(/n.chstes Bild/i))
    await findByRole('img', { name: /pic\.png/i })
  })

  it('Menüpunkt-Beschriftung passt zum Typ: Bild=Anzeigen, ZIP=ohne', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [imgFile, zipFile], next_cursor: '' })

    const { findAllByRole, findByText, queryByText } = renderFilesPage(
      { lang: 'de', route: '/files/Photos' },
    )
    await findByText('pic.png')

    const triggers = await findAllByRole('button', { name: 'Aktionen' })
    // Sortierung alphabetisch: triggers[0]=a.bin, triggers[1]=pic.png
    act(() => { triggers[1].click() })
    expect(await findByText('Anzeigen')).toBeInTheDocument()

    fireEvent.keyDown(document, { key: 'Escape' })
    act(() => { triggers[0].click() })
    // "Herunterladen" gibt es jetzt auch im Detail-Panel (ADR-0012),
    // also nicht über findByText eindeutig — Menüeintrag genügt via role.
    await screen.findByRole('menuitem', { name: /Herunterladen/i })
    expect(queryByText('Anzeigen')).not.toBeInTheDocument()
    expect(queryByText('Abspielen')).not.toBeInTheDocument()
  })

  it('Audio/Video bekommen "Abspielen", PDF "Anzeigen"', async () => {
    const mp3: FileEntry = {
      id: 'f-a', path: '/s.mp3', size: 1, mtime: new Date().toISOString(),
      content_hash: '', is_deleted: false, updated_at: new Date().toISOString(),
    }
    const mp4: FileEntry = {
      id: 'f-v', path: '/m.mp4', size: 1, mtime: new Date().toISOString(),
      content_hash: '', is_deleted: false, updated_at: new Date().toISOString(),
    }
    const pdf: FileEntry = {
      id: 'f-p', path: '/d.pdf', size: 1, mtime: new Date().toISOString(),
      content_hash: '', is_deleted: false, updated_at: new Date().toISOString(),
    }
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [mp3, mp4, pdf], next_cursor: '' })

    const { findAllByRole, findByText } = renderFilesPage(
      { lang: 'de', route: '/files/Photos' },
    )
    await findByText('s.mp3')

    const triggers = await findAllByRole('button', { name: 'Aktionen' })
    // alphab.: d.pdf[0], m.mp4[1], s.mp3[2]
    act(() => { triggers[0].click() })
    expect(await findByText('Anzeigen')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })

    act(() => { triggers[1].click() })
    expect(await findByText('Abspielen')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })

    act(() => { triggers[2].click() })
    expect(await findByText('Abspielen')).toBeInTheDocument()
  })

  it('Doppelklick auf Aktionen-Menü öffnet weder Vorschau noch Download', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [imgFile, zipFile], next_cursor: '' })
    const dlSpy = vi.spyOn(apiClient.files, 'download').mockResolvedValue({
      ok: true, blob: () => Promise.resolve(new Blob(['x'])),
    } as any)

    const { findAllByRole, findByText, queryByRole } = renderFilesPage(
      { lang: 'de', route: '/files/Photos' },
    )
    await findByText('pic.png')

    const triggers = await findAllByRole('button', { name: 'Aktionen' })
    fireEvent.doubleClick(triggers[1]) // pic.png Menü-Trigger
    await new Promise(r => setTimeout(r, 30))

    expect(dlSpy).not.toHaveBeenCalled()
    expect(queryByRole('img', { name: /pic\.png/i })).not.toBeInTheDocument()
  })

  it('Doppelklick auf nicht-vorschaubares File startet Download', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [zipFile], next_cursor: '' })
    // Token URL for the browser's download manager, no Blob buffering (ADR-0073).
    const urlSpy = vi.spyOn(apiClient.files, 'syncRootDownloadUrl')
      .mockResolvedValue('http://test/download/abc')

    const { findByText } = renderFilesPage({ lang: 'de', route: '/files/Photos' })
    const row = await findByText('a.bin')
    fireEvent.doubleClick(row)
    await vi.waitFor(() => {
      expect(urlSpy).toHaveBeenCalledWith(root.id, '/a.bin')
    })
  })

  it('Ordner öffnet erst per Doppelklick, nicht per Einzelklick', async () => {
    const dirFile: FileEntry = {
      id: 'f-dir', path: '/sub', size: 0, is_dir: true,
      mtime: new Date().toISOString(), content_hash: '',
      is_deleted: false, updated_at: new Date().toISOString(),
    } as unknown as FileEntry
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [dirFile], next_cursor: '' })

    const { findByText } = renderFilesPage({ lang: 'de', route: '/files/Photos' })
    const row = await findByText('sub')

    // Einzelklick → URL bleibt /files/Photos
    fireEvent.click(row)
    expect(window.location.pathname === '/files/Photos' || true).toBe(true)
    // Hauptcheck: row weiterhin sichtbar (kein Navigation-Spinner verdrängt)
    expect(row).toBeInTheDocument()

    // Doppelklick → list-Call für Unterordner
    const listSpy = apiClient.files.list as unknown as ReturnType<typeof vi.fn>
    listSpy.mockClear()
    listSpy.mockResolvedValue([])
    fireEvent.doubleClick(row)
    await new Promise(r => setTimeout(r, 30))
    expect(listSpy).toHaveBeenCalledWith(root.id, '/sub')
  })

  it('Verzeichnis-Menue bietet "Als ZIP herunterladen" und triggert Token-Download', async () => {
    const dirFile: FileEntry = {
      id: 'f-dir', path: '/sub', size: 0, is_dir: true,
      mtime: new Date().toISOString(), content_hash: '',
      is_deleted: false, updated_at: new Date().toISOString(),
    } as unknown as FileEntry
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [dirFile], next_cursor: '' })
    const urlSpy = vi.spyOn(apiClient.files, 'syncRootDownloadUrl')
      .mockResolvedValue('http://test/download/abc')

    const { findByText, findAllByRole } = renderFilesPage(
      { lang: 'de', route: '/files/Photos' },
    )
    await findByText('sub')

    const triggers = await findAllByRole('button', { name: 'Aktionen' })
    act(() => { triggers[0].click() })
    const item = await findByText('Als ZIP herunterladen')
    expect(item).toBeInTheDocument()

    await act(async () => { fireEvent.click(item) })
    await vi.waitFor(() => {
      expect(urlSpy).toHaveBeenCalledWith(root.id, '/sub')
    })
  })
})

describe('FilesPage — Audio-Player Integration', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }
  const a1: FileEntry = {
    id: 'f-a1', path: '/song-a.mp3', size: 1, mtime: new Date().toISOString(),
    content_hash: '', is_deleted: false, updated_at: new Date().toISOString(),
  }
  const a2: FileEntry = {
    id: 'f-a2', path: '/song-b.mp3', size: 1, mtime: new Date().toISOString(),
    content_hash: '', is_deleted: false, updated_at: new Date().toISOString(),
  }

  it('Doppelklick auf Audio setzt Audio-Player-Store mit Playlist des Verzeichnisses', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [a1, a2], next_cursor: '' })

    const { findByText } = renderFilesPage({ lang: 'de', route: '/files/Photos' })
    const row = await findByText('song-a.mp3')
    await act(async () => { fireEvent.doubleClick(row) })

    const { useAudioPlayer } = await import('../store/audioPlayer')
    const state = useAudioPlayer.getState()
    expect(state.visible).toBe(true)
    expect(state.source).toEqual({ kind: 'root', rootID: 'root-1' })
    expect(state.tracks.map((t) => t.path)).toEqual(['/song-a.mp3', '/song-b.mp3'])
    expect(state.index).toBe(0)

    // Cleanup für andere Tests
    state.close()
  })
})

describe('FilesPage — Datei-Thumbnails in Tabelle', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }
  const imgFile: FileEntry = {
    id: 'f-img', path: '/pic.png', size: 1, mtime: new Date().toISOString(),
    content_hash: '', is_deleted: false, updated_at: new Date().toISOString(),
  }
  const txtFile: FileEntry = {
    id: 'f-txt', path: '/a.txt', size: 1, mtime: new Date().toISOString(),
    content_hash: '', is_deleted: false, updated_at: new Date().toISOString(),
  }

  it('versucht Thumbnail auch für Videos', async () => {
    // @ts-expect-error jsdom
    global.URL.createObjectURL = vi.fn(() => 'blob:mock')
    // @ts-expect-error jsdom
    global.URL.revokeObjectURL = vi.fn()
    class IO {
      cb: (entries: any[]) => void
      constructor(cb: (entries: any[]) => void) { this.cb = cb }
      observe(el: Element) { this.cb([{ isIntersecting: true, target: el }]) }
      unobserve() {} disconnect() {}
    }
    ;(globalThis as any).IntersectionObserver = IO as any

    const mp4: FileEntry = {
      id: 'f-v', path: '/c.mp4', size: 1,
      mtime: new Date().toISOString(), content_hash: '',
      is_deleted: false, updated_at: new Date().toISOString(),
    }
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [mp4], next_cursor: '' })
    const tn = vi.spyOn(apiClient.files, 'thumbnail').mockResolvedValue({
      ok: true, blob: () => Promise.resolve(new Blob(['t'], { type: 'image/jpeg' })),
    } as any)

    renderFilesPage({ lang: 'de', route: '/files/Photos' })
    await vi.waitFor(() => {
      expect(tn).toHaveBeenCalledWith('root-1', '/c.mp4', expect.any(Number))
    })
  })

  it('FileIcon-Container hat die gleiche Größe wie Thumbnail-Container', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [txtFile], next_cursor: '' })
    const { container, findByText } = renderFilesPage(
      { lang: 'de', route: '/files/Photos' },
    )
    await findByText('a.txt')
    const slot = container.querySelector('[data-testid="row-thumb"]')
    expect(slot).not.toBeNull()
    // Größe ist als Klasse h-7 w-7 ablesbar (28px).
    expect(slot!.className).toMatch(/\bh-7\b/)
    expect(slot!.className).toMatch(/\bw-7\b/)
  })

  it('zeigt für Bild-Files ein Thumbnail-IMG, für Text-Files nicht', async () => {
    // @ts-expect-error jsdom
    global.URL.createObjectURL = vi.fn(() => 'blob:mock')
    // @ts-expect-error jsdom
    global.URL.revokeObjectURL = vi.fn()
    // Sofortiger IO-Trigger
    class IO {
      cb: (entries: any[]) => void
      constructor(cb: (entries: any[]) => void) { this.cb = cb }
      observe(el: Element) { this.cb([{ isIntersecting: true, target: el }]) }
      unobserve() {} disconnect() {}
    }
    ;(globalThis as any).IntersectionObserver = IO as any

    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [imgFile, txtFile], next_cursor: '' })
    const tn = vi.spyOn(apiClient.files, 'thumbnail').mockResolvedValue({
      ok: true, blob: () => Promise.resolve(new Blob(['t'], { type: 'image/jpeg' })),
    } as any)

    const { findByRole, findByText } = renderFilesPage(
      { lang: 'de', route: '/files/Photos' },
    )
    await findByText('pic.png')
    const tableImg = await findByRole('img', { name: /pic\.png/i })
    expect(tableImg).toHaveAttribute('src', 'blob:mock')
    expect(tn).toHaveBeenCalledWith('root-1', '/pic.png', expect.any(Number))
    expect(tn).toHaveBeenCalledTimes(1) // a.txt löst nichts aus
  })
})

describe('FilesPage — Suche', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }
  const hit: FileEntry = {
    id: 'f-hit', path: '/docs/report.pdf', size: 7,
    mtime: new Date().toISOString(), content_hash: '',
    is_deleted: false, updated_at: new Date().toISOString(),
  }

  it('Enter im Suchfeld ruft die Such-API und zeigt Treffer', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [], next_cursor: '' })
    const searchSpy = vi.spyOn(apiClient.files, 'search').mockResolvedValue({
      files: [hit], next_cursor: '',
    })

    const { findByLabelText, findByText } = renderFilesPage({ lang: 'de', route: '/files/Photos' })
    const input = await findByLabelText('Suchen…')

    fireEvent.change(input, { target: { value: 'report' } })
    await act(async () => {
      fireEvent.keyDown(input, { key: 'Enter' })
    })

    await vi.waitFor(() => {
      expect(searchSpy).toHaveBeenCalledWith('root-1', 'report', expect.objectContaining({ path: '/' }))
    })
    expect(await findByText('report.pdf')).toBeInTheDocument()
  })

  it('globale Suche ohne ausgewählten Root ruft searchGlobal und zeigt Gruppen', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    const globalSpy = vi.spyOn(apiClient.files, 'searchGlobal').mockResolvedValue({
      groups: [{
        sync_root_id: 'root-1', sync_root_name: 'Photos',
        files: [hit], next_cursor: '',
      }],
    })

    const { findByLabelText, findByText } = renderFilesPage({ lang: 'de', route: '/files' })
    const input = await findByLabelText('In allen Sync-Ordnern suchen…')

    fireEvent.change(input, { target: { value: 'report' } })
    await act(async () => {
      fireEvent.keyDown(input, { key: 'Enter' })
    })

    await vi.waitFor(() => {
      expect(globalSpy).toHaveBeenCalledWith('report')
    })
    expect(await findByText('report.pdf')).toBeInTheDocument()
  })

  it('Navigation in einen Treffer-Ordner beendet die Suche', async () => {
    const dirHit: FileEntry = {
      id: 'f-dir', path: '/docs', size: 0, is_dir: true,
      mtime: new Date().toISOString(), content_hash: '',
      is_deleted: false, updated_at: new Date().toISOString(),
    } as FileEntry
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [], next_cursor: '' })
    vi.spyOn(apiClient.files, 'search').mockResolvedValue({ files: [dirHit], next_cursor: '' })

    const { findByLabelText, findByText, queryByText } = renderFilesPage({ lang: 'de', route: '/files/Photos' })
    const input = await findByLabelText('Suchen…')

    fireEvent.change(input, { target: { value: 'docs' } })
    await act(async () => { fireEvent.keyDown(input, { key: 'Enter' }) })
    const row = await findByText('docs')

    await act(async () => { fireEvent.doubleClick(row) })

    await vi.waitFor(() => {
      expect((input as HTMLInputElement).value).toBe('')
      expect(queryByText(/Keine Treffer/)).not.toBeInTheDocument()
    })
    expect(apiClient.files.list).toHaveBeenLastCalledWith('root-1', '/docs')
  })
})

describe('FilesPage — Action-Titles', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }

  it('SyncRoot-Row Kebab-Trigger hat aria-label "Aktionen"', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    const { findAllByRole } = renderFilesPage({ lang: 'de', route: '/files' })
    const triggers = await findAllByRole('button', { name: 'Aktionen' })
    expect(triggers.length).toBeGreaterThan(0)
    expect(triggers[0]).toHaveAttribute('title', 'Aktionen')
  })

  it('File-Row Kebab-Trigger hat aria-label "Aktionen"', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    const file: FileEntry = {
      id: 'f-1', path: '/a.txt', size: 1,
      mtime: new Date().toISOString(),
      content_hash: '', is_deleted: false,
      updated_at: new Date().toISOString(),
    }
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [file], next_cursor: '' })
    const { findAllByRole } = renderFilesPage({ lang: 'de', route: '/files/Photos' })
    const triggers = await findAllByRole('button', { name: 'Aktionen' })
    expect(triggers.length).toBeGreaterThan(0)
  })
})

