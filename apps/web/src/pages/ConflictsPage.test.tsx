import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { renderWithI18n } from '../test/renderWithI18n'
import ConflictsPage from './ConflictsPage'
import { apiClient } from '../store/auth'

const sseState = vi.hoisted(() => ({
  handlers: {} as Record<string, (d: unknown) => void>,
}))
vi.mock('../hooks/useSSE', () => ({
  useSSE: (h: Record<string, (d: unknown) => void>) => {
    sseState.handlers = h
  },
}))

beforeEach(() => {
  vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([])
  vi.spyOn(apiClient.files, 'listConflicts').mockResolvedValue([])
  vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([])
  vi.spyOn(apiClient.sharedMounts, 'conflicts').mockResolvedValue([])
  vi.spyOn(console, 'error').mockImplementation(() => {})
  sseState.handlers = {}
})

describe('ConflictsPage — DE', () => {
  it('rendert Header und Empty-State', async () => {
    const { findByRole, findByText } = renderWithI18n(<ConflictsPage />, { lang: 'de' })
    expect(await findByRole('heading', { level: 1, name: 'Konflikte' })).toBeInTheDocument()
    expect(await findByText('Keine offenen Konflikte')).toBeInTheDocument()
  })
})

describe('ConflictsPage — EN', () => {
  it('renders header and empty state', async () => {
    const { findByRole, findByText } = renderWithI18n(<ConflictsPage />, { lang: 'en' })
    expect(await findByRole('heading', { level: 1, name: 'Conflicts' })).toBeInTheDocument()
    expect(await findByText('No open conflicts')).toBeInTheDocument()
  })
})

describe('ConflictsPage — SSE optimistic patch', () => {
  it('inserts conflict on conflict_added', async () => {
    const { findByText, queryByText } = renderWithI18n(<ConflictsPage />, { lang: 'de' })
    await findByText('Keine offenen Konflikte')

    await act(async () => {
      sseState.handlers.conflict_added?.({
        type: 'conflict_added', version: 2,
        data: {
          conflict_id: 'c-1',
          file_id: 'f-1',
          sync_root_id: 'r-1',
          conflict_path: '/foo.conflict-2026.txt',
          original_path: '/foo.txt',
          created_at: new Date().toISOString(),
        },
      })
    })
    expect(await findByText('/foo.conflict-2026.txt')).toBeInTheDocument()

    expect(queryByText('Keine offenen Konflikte')).not.toBeInTheDocument()
  })

  it('verschiebt Konflikt von Offen nach Aufgelöst bei conflict_resolved', async () => {
    const { findByText, queryByText } = renderWithI18n(<ConflictsPage />, { lang: 'de' })
    await findByText('Keine offenen Konflikte')

    await act(async () => {
      sseState.handlers.conflict_added?.({
        type: 'conflict_added', version: 2,
        data: {
          conflict_id: 'c-1',
          file_id: 'f-1',
          sync_root_id: 'r-1',
          conflict_path: '/foo.conflict-2026.txt',
          original_path: '/foo.txt',
          created_at: new Date().toISOString(),
        },
      })
    })
    expect(await findByText('Offen (1)')).toBeInTheDocument()

    await act(async () => {
      sseState.handlers.conflict_resolved?.({
        conflict_id: 'c-1',
        file_id: 'f-1',
        sync_root_id: 'r-1',
        file_path: '/foo.txt',
      })
    })

    expect(await findByText('Keine offenen Konflikte')).toBeInTheDocument()
    expect(await findByText('Aufgelöst (1)')).toBeInTheDocument()
    expect(await findByText('/foo.conflict-2026.txt')).toBeInTheDocument()
  })
})

describe('ConflictsPage — Action-Titles', () => {
  it('Download- und Resolve-Buttons haben title-Attribut', async () => {
    const { findAllByRole, findByText } = renderWithI18n(<ConflictsPage />, { lang: 'de' })
    await findByText('Keine offenen Konflikte')

    await act(async () => {
      sseState.handlers.conflict_added?.({
        type: 'conflict_added', version: 2,
        data: {
          conflict_id: 'c-1',
          file_id: 'f-1',
          sync_root_id: 'r-1',
          conflict_path: '/foo.conflict.txt',
          original_path: '/foo.txt',
          created_at: new Date().toISOString(),
        },
      })
    })

    const download = await findAllByRole('button', { name: 'Herunterladen' })
    expect(download[0].closest('[title="Herunterladen"]')).not.toBeNull()
    const resolve = await findAllByRole('button', { name: 'Auflösen' })
    expect(resolve[0].closest('[title="Auflösen"]')).not.toBeNull()
  })
})

describe('ConflictsPage — Shared Mounts (ADR-0011)', () => {
  const mount = {
    id: 'share-1', name: '2024', path: '/2024', is_file: false,
    can_write: true, expires_at: null,
    owner: { id: 'u-alice', username: 'alice', avatar_url: null },
  }

  it('lädt Mount-Konflikte mit Geber-Label und erlaubt Auflösen bei can_write', async () => {
    vi.mocked(apiClient.sharedMounts.list).mockResolvedValue([mount])
    vi.mocked(apiClient.sharedMounts.conflicts).mockResolvedValue([{
      id: 'mc-1', file_id: 'mf-1', conflict_path: '/x.conflict.txt',
      created_at: new Date().toISOString(),
    }])

    const { findByText, findAllByRole } = renderWithI18n(<ConflictsPage />, { lang: 'de' })
    // Geber-Kontext im Root-Label.
    expect(await findByText('2024 (alice)')).toBeInTheDocument()
    expect(await findByText('/x.conflict.txt')).toBeInTheDocument()
    // can_write -> Resolve-Button da.
    const resolve = await findAllByRole('button', { name: 'Auflösen' })
    expect(resolve.length).toBeGreaterThan(0)
  })

  it('zeigt keinen Auflösen-Button für read-only Mount-Konflikte', async () => {
    vi.mocked(apiClient.sharedMounts.list).mockResolvedValue([{ ...mount, can_write: false }])
    vi.mocked(apiClient.sharedMounts.conflicts).mockResolvedValue([{
      id: 'mc-2', file_id: 'mf-2', conflict_path: '/ro.conflict.txt',
      created_at: new Date().toISOString(),
    }])

    const { findByText, queryAllByRole } = renderWithI18n(<ConflictsPage />, { lang: 'de' })
    await findByText('/ro.conflict.txt')
    // read-only -> kein Resolve, aber Download bleibt.
    expect(queryAllByRole('button', { name: 'Auflösen' })).toHaveLength(0)
  })

  it('resolved Mount-Konflikt über den Mount-Endpoint, nicht den Eigen-Root', async () => {
    vi.mocked(apiClient.sharedMounts.list).mockResolvedValue([mount])
    vi.mocked(apiClient.sharedMounts.conflicts).mockResolvedValue([{
      id: 'mc-3', file_id: 'mf-3', conflict_path: '/y.conflict.txt',
      created_at: new Date().toISOString(),
    }])
    vi.spyOn(apiClient.sharedMounts, 'conflictVersions').mockResolvedValue({
      id: 'mc-3', file_id: 'mf-3', file_path: '/y.txt',
      conflict_path: '/y.conflict.txt',
      created_at: '2026-06-01T10:00:00Z',
      current:  { size: 4, mtime: '2026-06-01T09:00:00Z' },
      conflict: { size: 9, mtime: null, created_at: '2026-06-01T10:00:00Z' },
    })
    const mountResolve = vi.spyOn(apiClient.sharedMounts, 'resolveConflict').mockResolvedValue(undefined)
    const ownResolve = vi.spyOn(apiClient.files, 'resolveConflict').mockResolvedValue(undefined)

    const { findAllByRole, findByRole } = renderWithI18n(<ConflictsPage />, { lang: 'de' })
    const resolve = await findAllByRole('button', { name: 'Auflösen' })
    await act(async () => { resolve[0].click() })

    // The dialog resolves through the mount-scoped endpoint.
    const keepCurrent = await findByRole('button', { name: 'Aktuelle behalten' })
    await act(async () => { keepCurrent.click() })

    expect(mountResolve).toHaveBeenCalledWith('share-1', 'mc-3', 'keep_current')
    expect(ownResolve).not.toHaveBeenCalled()
  })
})

describe('ConflictsPage — Resolve-Dialog (Strategien)', () => {
  const versions = {
    id: 'c-1', file_id: 'f-1', file_path: '/foo.txt',
    conflict_path: '/foo.conflict-2026.txt',
    created_at: '2026-06-01T10:00:00Z',
    current:  { size: 6,  mtime: '2026-06-01T09:00:00Z' },
    conflict: { size: 11, mtime: null, created_at: '2026-06-01T10:00:00Z' },
  }

  async function openDialogForOwnConflict() {
    vi.spyOn(apiClient.files, 'getConflict').mockResolvedValue(versions)
    const utils = renderWithI18n(<ConflictsPage />, { lang: 'de' })
    await utils.findByText('Keine offenen Konflikte')
    await act(async () => {
      sseState.handlers.conflict_added?.({
        type: 'conflict_added', version: 2,
        data: {
          conflict_id: 'c-1',
          file_id: 'f-1',
          sync_root_id: 'r-1',
          conflict_path: '/foo.conflict-2026.txt',
          original_path: '/foo.txt',
          created_at: new Date().toISOString(),
        },
      })
    })
    const resolveBtns = await utils.findAllByRole('button', { name: 'Auflösen' })
    await act(async () => { resolveBtns[0].click() })
    return utils
  }

  it('zeigt beide Versionen mit Metadaten im Dialog', async () => {
    const { findByRole, findByText } = await openDialogForOwnConflict()
    expect(await findByRole('dialog')).toBeInTheDocument()
    expect(await findByText('Aktuelle Version')).toBeInTheDocument()
    expect(await findByText('Konfliktversion')).toBeInTheDocument()
    // Exact sizes from the versions endpoint (6 / 11 bytes).
    expect(await findByText('6 Byte')).toBeInTheDocument()
    expect(await findByText('11 Byte')).toBeInTheDocument()
  })

  it('Konfliktversion übernehmen ruft resolve mit keep_conflict', async () => {
    const resolve = vi.spyOn(apiClient.files, 'resolveConflict').mockResolvedValue(undefined)
    const { findByRole } = await openDialogForOwnConflict()
    const btn = await findByRole('button', { name: 'Konfliktversion übernehmen' })
    await act(async () => { btn.click() })
    expect(resolve).toHaveBeenCalledWith('c-1', 'keep_conflict')
  })

  it('Beide behalten ruft resolve mit keep_both', async () => {
    const resolve = vi.spyOn(apiClient.files, 'resolveConflict').mockResolvedValue(undefined)
    const { findByRole } = await openDialogForOwnConflict()
    const btn = await findByRole('button', { name: 'Beide behalten' })
    await act(async () => { btn.click() })
    expect(resolve).toHaveBeenCalledWith('c-1', 'keep_both')
  })
})
