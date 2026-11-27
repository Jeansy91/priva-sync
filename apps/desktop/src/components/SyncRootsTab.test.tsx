import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../test/renderWithI18n'
import { useAppStore } from '../store'
import type { SyncRootEntry, ServerSyncRoot, AvailableMount } from '../tauri'

const listSyncRoots = vi.fn()
const listServerSyncRoots = vi.fn()
const listAvailableMounts = vi.fn()
const getRemoveSyncRootOptions = vi.fn()
const removeSyncRoot = vi.fn()
const removeMount = vi.fn()
const setDownloadMode = vi.fn()
const resumeSyncRoot = vi.fn()
const pauseSyncRoot = vi.fn()
const forceSync = vi.fn()
const getSyncStatus = vi.fn()

vi.mock('../tauri', () => ({
  cmd: {
    listSyncRoots: (...a: unknown[]) => listSyncRoots(...a),
    listServerSyncRoots: (...a: unknown[]) => listServerSyncRoots(...a),
    listAvailableMounts: (...a: unknown[]) => listAvailableMounts(...a),
    getRemoveSyncRootOptions: (...a: unknown[]) => getRemoveSyncRootOptions(...a),
    removeSyncRoot: (...a: unknown[]) => removeSyncRoot(...a),
    removeMount: (...a: unknown[]) => removeMount(...a),
    setDownloadMode: (...a: unknown[]) => setDownloadMode(...a),
    resumeSyncRoot: (...a: unknown[]) => resumeSyncRoot(...a),
    pauseSyncRoot: (...a: unknown[]) => pauseSyncRoot(...a),
    forceSync: (...a: unknown[]) => forceSync(...a),
    getSyncStatus: (...a: unknown[]) => getSyncStatus(...a),
  },
  onSharesChanged: () => Promise.resolve(() => {}),
}))

vi.mock('@tauri-apps/plugin-dialog', () => ({ open: vi.fn() }))

import SyncRootsTab from './SyncRootsTab'

const owned: SyncRootEntry = {
  id: 'o1', name: 'Fotos', local_path: '/Fotos', server_root_id: 'o1',
  download_mode: 'eager', read_only: false, paused: false, kind: 'owned',
}
const mount: SyncRootEntry = {
  id: 'm1', name: 'AlicePics', local_path: '/AlicePics', server_root_id: 's-alice',
  download_mode: 'eager', read_only: false, paused: false, kind: 'mount',
}
const addableServerRoot: ServerSyncRoot = { id: 'sr1', name: 'Projekte', path: '/Projekte' }
const availableMount: AvailableMount = {
  share_id: 's-bob', name: 'BobDocs', path: '/Docs', can_write: true, owner_username: 'bob',
}

function pos(a: HTMLElement, b: HTMLElement): boolean {
  return Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)
}

beforeEach(() => {
  useAppStore.setState({ syncRoots: [], config: null, rootProgress: {} })
  listSyncRoots.mockReset().mockResolvedValue([owned, mount])
  listServerSyncRoots.mockReset().mockResolvedValue([addableServerRoot])
  listAvailableMounts.mockReset().mockResolvedValue([availableMount])
  getRemoveSyncRootOptions.mockReset().mockResolvedValue({ dehydrated_count: 0, dehydrated_bytes: 0 })
  removeSyncRoot.mockReset().mockResolvedValue(undefined)
  removeMount.mockReset().mockResolvedValue(undefined)
  setDownloadMode.mockReset().mockResolvedValue(undefined)
  resumeSyncRoot.mockReset().mockResolvedValue(undefined)
  pauseSyncRoot.mockReset().mockResolvedValue(undefined)
  forceSync.mockReset().mockResolvedValue(undefined)
  getSyncStatus.mockReset().mockResolvedValue({ status: 'idle', pending_count: 0 })
})

describe('SyncRootsTab grouping', () => {
  it('keeps the Sync group and Mount group separate, added before available', async () => {
    renderWithI18n(<SyncRootsTab />)

    // Wait for the async loads (sync roots + available mounts) to settle.
    await screen.findByText('BobDocs')

    const fotos    = screen.getByText('Fotos')          // added sync folder
    const availSync = screen.getByText('Verfügbare Sync-Ordner')
    const projekte = screen.getByText('Projekte')       // available sync folder
    const mountHdr = screen.getByText('Mit mir geteilt')
    const alice    = screen.getByText('AlicePics')      // added mount
    const availMnt = screen.getByText('Verfügbare Freigaben')
    const bob      = screen.getByText('BobDocs')        // available mount

    // Sync group: added folder before the "available" sub-heading + its rows.
    expect(pos(fotos, availSync)).toBe(true)
    expect(pos(availSync, projekte)).toBe(true)

    // The whole Sync group sits above the Mount group.
    expect(pos(projekte, mountHdr)).toBe(true)

    // Mount group: added mount before the "available" sub-heading + its rows.
    expect(pos(mountHdr, alice)).toBe(true)
    expect(pos(alice, availMnt)).toBe(true)
    expect(pos(availMnt, bob)).toBe(true)
  })

  it('orders activated sync roots alphabetically regardless of load order', async () => {
    const mk = (name: string): SyncRootEntry => ({
      id: name, name, local_path: '/' + name, server_root_id: name,
      download_mode: 'eager', read_only: false, paused: false, kind: 'owned',
    })
    listSyncRoots.mockResolvedValue([mk('Zebra'), mk('Apfel'), mk('Mango')])
    listServerSyncRoots.mockResolvedValue([])
    listAvailableMounts.mockResolvedValue([])

    renderWithI18n(<SyncRootsTab />)
    await screen.findByText('Apfel')

    expect(pos(screen.getByText('Apfel'), screen.getByText('Mango'))).toBe(true)
    expect(pos(screen.getByText('Mango'), screen.getByText('Zebra'))).toBe(true)
  })
})

describe('SyncRootsTab empty state', () => {
  it('shows the empty-state CTA when nothing is added or addable', async () => {
    listSyncRoots.mockResolvedValue([])
    listServerSyncRoots.mockResolvedValue([])
    listAvailableMounts.mockResolvedValue([])

    renderWithI18n(<SyncRootsTab />)

    expect(await screen.findByText('Noch keine Ordner synchronisiert.')).toBeInTheDocument()
    fireEvent.click(screen.getByText('Ersten Ordner hinzufügen'))
  })
})

describe('SyncRootsTab force sync', () => {
  it('triggers a sync and refreshes the status', async () => {
    renderWithI18n(<SyncRootsTab />)
    await screen.findByText('Fotos')

    fireEvent.click(screen.getByTitle('Jetzt synchronisieren'))

    await waitFor(() => expect(forceSync).toHaveBeenCalled())
    await waitFor(() => expect(getSyncStatus).toHaveBeenCalled())
  })
})

describe('SyncRootsTab remove flow', () => {
  it('opens the confirm dialog, loads remove options and removes an owned root', async () => {
    renderWithI18n(<SyncRootsTab />)
    await screen.findByText('Fotos')

    fireEvent.click(screen.getAllByTitle('Entfernen')[0])
    await waitFor(() => expect(getRemoveSyncRootOptions).toHaveBeenCalledWith('o1'))

    const confirmButtons = await screen.findAllByRole('button', { name: 'Entfernen' })
    fireEvent.click(confirmButtons[confirmButtons.length - 1])

    await waitFor(() => expect(removeSyncRoot).toHaveBeenCalledWith('o1'))
    await waitFor(() => expect(listSyncRoots).toHaveBeenCalledTimes(2))
  })

  it('removes a mount by its server_root_id instead of its local id', async () => {
    renderWithI18n(<SyncRootsTab />)
    await screen.findByText('AlicePics')

    const removeButtons = screen.getAllByTitle('Entfernen')
    fireEvent.click(removeButtons[removeButtons.length - 1])
    await waitFor(() => expect(getRemoveSyncRootOptions).toHaveBeenCalledWith('m1'))

    const confirmButtons = await screen.findAllByRole('button', { name: 'Entfernen' })
    fireEvent.click(confirmButtons[confirmButtons.length - 1])

    await waitFor(() => expect(removeMount).toHaveBeenCalledWith('s-alice'))
  })

  it('falls back to a zero-count options object when loading remove options fails', async () => {
    getRemoveSyncRootOptions.mockRejectedValue(new Error('boom'))
    renderWithI18n(<SyncRootsTab />)
    await screen.findByText('Fotos')

    fireEvent.click(screen.getAllByTitle('Entfernen')[0])

    await waitFor(() => expect(screen.queryByText('Prüfe lokale Dateien…')).not.toBeInTheDocument())
    expect(await screen.findAllByRole('button', { name: 'Entfernen' })).not.toHaveLength(0)
  })

  it('surfaces an error banner when removal itself fails', async () => {
    removeSyncRoot.mockRejectedValue(new Error('server unreachable'))
    renderWithI18n(<SyncRootsTab />)
    await screen.findByText('Fotos')

    fireEvent.click(screen.getAllByTitle('Entfernen')[0])
    // Wait for the dialog's own confirm button (3rd "Entfernen"-named element,
    // alongside the two row trash icons) to mount once options resolve.
    await waitFor(() => expect(screen.getAllByRole('button', { name: 'Entfernen' })).toHaveLength(3))
    const confirmButtons = screen.getAllByRole('button', { name: 'Entfernen' })
    fireEvent.click(confirmButtons[confirmButtons.length - 1])

    await waitFor(() => expect(screen.getByText(/server unreachable/)).toBeInTheDocument())
  })

  it('download-first switches to eager mode, forces a sync and shows the info banner', async () => {
    // download-first is only offered when the root isn't already eager.
    listSyncRoots.mockResolvedValue([{ ...owned, download_mode: 'on_demand' }, mount])
    getRemoveSyncRootOptions.mockResolvedValue({ dehydrated_count: 2, dehydrated_bytes: 2048 })
    renderWithI18n(<SyncRootsTab />)
    await screen.findByText('Fotos')

    fireEvent.click(screen.getAllByTitle('Entfernen')[0])
    fireEvent.click(await screen.findByText('Erst alles herunterladen'))

    await waitFor(() => expect(setDownloadMode).toHaveBeenCalledWith('o1', 'eager'))
    await waitFor(() => expect(forceSync).toHaveBeenCalled())
    expect(await screen.findByText('Download gestartet. Du kannst den Ordner entfernen, sobald der Sync abgeschlossen ist.')).toBeInTheDocument()
  })
})

describe('SyncRootsTab pause/resume and download-mode toggle', () => {
  it('pauses an active root', async () => {
    renderWithI18n(<SyncRootsTab />)
    await screen.findByText('Fotos')

    fireEvent.click(screen.getAllByTitle('Pausieren')[0])

    await waitFor(() => expect(pauseSyncRoot).toHaveBeenCalledWith('o1'))
  })

  it('resumes a paused root', async () => {
    listSyncRoots.mockResolvedValue([{ ...owned, paused: true }, mount])
    renderWithI18n(<SyncRootsTab />)
    await screen.findByText('Fotos')

    fireEvent.click(screen.getByTitle('Fortsetzen'))

    await waitFor(() => expect(resumeSyncRoot).toHaveBeenCalledWith('o1'))
  })

  it('shows an error banner when pausing fails', async () => {
    pauseSyncRoot.mockRejectedValue(new Error('locked'))
    renderWithI18n(<SyncRootsTab />)
    await screen.findByText('Fotos')

    fireEvent.click(screen.getAllByTitle('Pausieren')[0])

    await waitFor(() => expect(screen.getByText(/locked/)).toBeInTheDocument())
  })

  it('toggles download mode and surfaces an error on failure', async () => {
    useAppStore.setState({ config: { supports_on_demand: true } as any })
    setDownloadMode.mockRejectedValue(new Error('unsupported'))
    renderWithI18n(<SyncRootsTab />)
    await screen.findByText('Fotos')

    fireEvent.click(screen.getAllByTitle('Sofort (Klicken für On-Demand)')[0])

    await waitFor(() => expect(setDownloadMode).toHaveBeenCalledWith('o1', 'on_demand'))
    await waitFor(() => expect(screen.getByText(/unsupported/)).toBeInTheDocument())
  })
})
