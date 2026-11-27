import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent, act } from '@testing-library/react'
import { renderWithI18n } from './test/renderWithI18n'
import { useAppStore } from './store'

const getConfig          = vi.fn()
const getSyncStatus       = vi.fn()
const listSyncRoots       = vi.fn()
const confirmBulkDelete   = vi.fn()
const restoreLocalFiles   = vi.fn()
const installUpdate       = vi.fn()
const getUpdateStage      = vi.fn()

type Listener<T> = ((arg: T) => void) | undefined

let statusListener:      Listener<unknown>
let conflictListener:    Listener<unknown>
let sessionListener:     (() => void) | undefined
let progressListener:    Listener<unknown>
let bulkDeleteListener:  Listener<{ root_id: string; root_name: string; delete_count: number; total_count: number }>
let pathWarningListener: Listener<unknown>
let updateListener:      Listener<{ version: string; current: string; notes: string | null; release_date: string | null }>
let orphanedListener:    Listener<{ root_id: string; root_name: string; local_path: string }>
let renamedListener:     (() => void) | undefined
let trayNavigateListener: Listener<string>
let stageListener:        Listener<Record<string, unknown>>

// Every on*() unlisten call is tracked separately so cleanup-on-unmount can be asserted.
const unlistenFns = {
  status: vi.fn(), conflict: vi.fn(), session: vi.fn(), progress: vi.fn(),
  bulkDelete: vi.fn(), pathWarning: vi.fn(), update: vi.fn(), orphaned: vi.fn(), renamed: vi.fn(),
  trayNavigate: vi.fn(), stage: vi.fn(),
}

vi.mock('./tauri', () => ({
  cmd: {
    getConfig:          (...a: unknown[]) => getConfig(...a),
    getSyncStatus:       (...a: unknown[]) => getSyncStatus(...a),
    listSyncRoots:       (...a: unknown[]) => listSyncRoots(...a),
    confirmBulkDelete:   (...a: unknown[]) => confirmBulkDelete(...a),
    restoreLocalFiles:   (...a: unknown[]) => restoreLocalFiles(...a),
    installUpdate:       (...a: unknown[]) => installUpdate(...a),
    getUpdateStage:      (...a: unknown[]) => getUpdateStage(...a),
  },
  onSyncStatusChanged: (cb: Listener<unknown>) => { statusListener = cb; return Promise.resolve(unlistenFns.status) },
  onSyncConflict:      (cb: Listener<unknown>) => { conflictListener = cb; return Promise.resolve(unlistenFns.conflict) },
  onSessionExpired:    (cb: () => void)        => { sessionListener = cb; return Promise.resolve(unlistenFns.session) },
  onSyncProgress:      (cb: Listener<unknown>) => { progressListener = cb; return Promise.resolve(unlistenFns.progress) },
  onBulkDeleteWarning: (cb: Listener<any>)     => { bulkDeleteListener = cb; return Promise.resolve(unlistenFns.bulkDelete) },
  onPathWarning:       (cb: Listener<unknown>) => { pathWarningListener = cb; return Promise.resolve(unlistenFns.pathWarning) },
  onUpdateAvailable:   (cb: Listener<any>)     => { updateListener = cb; return Promise.resolve(unlistenFns.update) },
  onRootOrphaned:      (cb: Listener<any>)     => { orphanedListener = cb; return Promise.resolve(unlistenFns.orphaned) },
  onRootRenamed:       (cb: () => void)        => { renamedListener = cb; return Promise.resolve(unlistenFns.renamed) },
  onTrayNavigate:      (cb: Listener<string>)  => { trayNavigateListener = cb; return Promise.resolve(unlistenFns.trayNavigate) },
  onUpdateStage:       (cb: Listener<any>)     => { stageListener = cb; return Promise.resolve(unlistenFns.stage) },
}))

vi.mock('./pages/SetupPage', () => ({
  default: ({ onLogin, onOpenSettings }: { onLogin: () => void; onOpenSettings: () => void }) => (
    <div>
      setup-page
      <button onClick={onLogin}>do-login</button>
      <button onClick={onOpenSettings}>open-settings</button>
    </div>
  ),
}))

vi.mock('./pages/PreLoginSettingsPage', () => ({
  default: ({ onBack }: { onBack: () => void }) => (
    <div>
      pre-login-settings
      <button onClick={onBack}>back</button>
    </div>
  ),
}))

vi.mock('./pages/DashboardPage', () => ({ default: () => <div>dashboard-page</div> }))

import App from './App'

const loggedOutConfig = { server_url: '', username: '', is_logged_in: false, supports_on_demand: false }
const loggedInConfig  = { server_url: 'http://x', username: 'alice', is_logged_in: true, supports_on_demand: false }

beforeEach(() => {
  vi.clearAllMocks()
  statusListener = conflictListener = sessionListener = progressListener = undefined
  bulkDeleteListener = pathWarningListener = updateListener = orphanedListener = renamedListener = undefined
  trayNavigateListener = stageListener = undefined
  useAppStore.setState({ config: null, status: null, syncRoots: [], rootProgress: {}, pathWarnings: [] } as any)

  getConfig.mockResolvedValue(loggedOutConfig)
  getSyncStatus.mockResolvedValue({ pending_count: 0, conflict_count: 0, status: 'idle' })
  listSyncRoots.mockResolvedValue([])
  confirmBulkDelete.mockResolvedValue(undefined)
  restoreLocalFiles.mockResolvedValue(undefined)
  installUpdate.mockResolvedValue(undefined)
  getUpdateStage.mockResolvedValue({ stage: 'idle' })
})

async function renderLoggedIn() {
  getConfig.mockResolvedValue(loggedInConfig)
  renderWithI18n(<App />)
  await screen.findByText('dashboard-page')
}

describe('App', () => {
  it('shows a loading spinner before the initial fetches resolve', () => {
    let resolveConfig: (v: unknown) => void = () => {}
    getConfig.mockReturnValue(new Promise((resolve) => { resolveConfig = resolve }))
    const { container } = renderWithI18n(<App />)

    expect(container.querySelector('.animate-spin')).toBeInTheDocument()
    resolveConfig(loggedOutConfig)
  })

  it('covers the login screen while the startup update check runs', async () => {
    getUpdateStage.mockResolvedValue({ stage: 'checking' })
    renderWithI18n(<App />)

    await screen.findByText('Suche nach Updates…')
    // The page stays mounted behind the gate — the login form must keep what
    // was typed into it while a check runs.
    expect(screen.getByText('setup-page')).toBeInTheDocument()

    act(() => { stageListener?.({ stage: 'idle' }) })
    await waitFor(() => expect(screen.queryByText('Suche nach Updates…')).not.toBeInTheDocument())
  })

  it('shows download progress reported by the update_stage event', async () => {
    renderWithI18n(<App />)
    await screen.findByText('setup-page')

    act(() => {
      stageListener?.({ stage: 'downloading', version: '9.9.9', downloaded: 512 * 1024, total: 1024 * 1024 })
    })
    await screen.findByText('Update 9.9.9 wird geladen…')
    expect(screen.getByText('512 KB / 1.0 MB')).toBeInTheDocument()
  })

  it('renders SetupPage when the fetched config is logged out', async () => {
    renderWithI18n(<App />)
    await screen.findByText('setup-page')
    expect(getConfig).toHaveBeenCalled()
    expect(getSyncStatus).toHaveBeenCalled()
    expect(listSyncRoots).toHaveBeenCalled()
  })

  it('opens the settings page from SetupPage without a session', async () => {
    renderWithI18n(<App />)
    await screen.findByText('setup-page')

    fireEvent.click(screen.getByText('open-settings'))
    await screen.findByText('pre-login-settings')
    expect(screen.queryByText('setup-page')).not.toBeInTheDocument()

    fireEvent.click(screen.getByText('back'))
    await screen.findByText('setup-page')
  })

  it('opens the settings page when the tray navigates there while logged out', async () => {
    renderWithI18n(<App />)
    await screen.findByText('setup-page')
    await waitFor(() => expect(trayNavigateListener).toBeDefined())

    act(() => trayNavigateListener!('settings'))
    await screen.findByText('pre-login-settings')
  })

  it('leaves SetupPage alone for tray targets other than settings', async () => {
    renderWithI18n(<App />)
    await screen.findByText('setup-page')
    await waitFor(() => expect(trayNavigateListener).toBeDefined())

    act(() => trayNavigateListener!('conflicts'))
    await waitFor(() => expect(screen.getByText('setup-page')).toBeInTheDocument())
    expect(screen.queryByText('pre-login-settings')).not.toBeInTheDocument()
  })

  it('renders DashboardPage directly when already logged in', async () => {
    await renderLoggedIn()
    expect(screen.queryByText('setup-page')).not.toBeInTheDocument()
  })

  it('switches from SetupPage to DashboardPage once onLogin refetches the config', async () => {
    renderWithI18n(<App />)
    await screen.findByText('setup-page')

    getConfig.mockResolvedValue(loggedInConfig)
    fireEvent.click(screen.getByText('do-login'))

    await screen.findByText('dashboard-page')
  })

  it('stops loading and logs the error when the initial fetch fails', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    getConfig.mockRejectedValue(new Error('network down'))

    renderWithI18n(<App />)

    await screen.findByText('setup-page')
    expect(consoleError).toHaveBeenCalled()
    consoleError.mockRestore()
  })

  it('updates the store status on a sync_status_changed event', async () => {
    await renderLoggedIn()
    await waitFor(() => expect(statusListener).toBeDefined())

    act(() => (statusListener as (s: unknown) => void)({ status: 'syncing', pending_count: 2, conflict_count: 0 }))

    expect(useAppStore.getState().status).toEqual({ status: 'syncing', pending_count: 2, conflict_count: 0 })
  })

  it('refetches sync status on a sync_conflict event', async () => {
    const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await renderLoggedIn()
    await waitFor(() => expect(conflictListener).toBeDefined())

    getSyncStatus.mockResolvedValue({ status: 'conflict', pending_count: 0, conflict_count: 1 })
    act(() => (conflictListener as (e: unknown) => void)({ path: '/a.txt', conflict_copy: '/a (Konflikt).txt' }))

    await waitFor(() => expect(useAppStore.getState().status).toEqual({ status: 'conflict', pending_count: 0, conflict_count: 1 }))
    expect(consoleWarn).toHaveBeenCalled()
    consoleWarn.mockRestore()
  })

  it('re-fetches the config on session_expired, keeping server_url/username for the login form', async () => {
    await renderLoggedIn()
    await waitFor(() => expect(sessionListener).toBeDefined())

    // server_url/username survive logout server-side (only the access token
    // is cleared), so the re-fetched config still carries them.
    const loggedOutButKnown = { ...loggedInConfig, is_logged_in: false }
    getConfig.mockResolvedValue(loggedOutButKnown)

    act(() => (sessionListener as () => void)())

    await waitFor(() => expect(useAppStore.getState().config).toEqual(loggedOutButKnown))
  })

  it('forwards sync_progress events to the store', async () => {
    await renderLoggedIn()
    await waitFor(() => expect(progressListener).toBeDefined())

    const evt = { root_id: 'r1', current: 1, total: 2, file: '/a.txt', action: 'upload' }
    act(() => (progressListener as (e: unknown) => void)(evt))

    expect(useAppStore.getState().rootProgress).toEqual({ r1: { '/a.txt': evt } })
  })

  it('adds path warnings to the store', async () => {
    await renderLoggedIn()
    await waitFor(() => expect(pathWarningListener).toBeDefined())

    const warning = { root_id: 'r1', path: '/too/long', code: 'path_too_long', params: { len: 300 }, blocking: false }
    act(() => (pathWarningListener as (w: unknown) => void)(warning))

    expect(useAppStore.getState().pathWarnings).toEqual([warning])
  })

  it('refreshes sync roots when a root is orphaned server-side', async () => {
    const consoleInfo = vi.spyOn(console, 'info').mockImplementation(() => {})
    await renderLoggedIn()
    await waitFor(() => expect(orphanedListener).toBeDefined())

    listSyncRoots.mockResolvedValue([{ id: 'r1' }])
    act(() => (orphanedListener as (e: unknown) => void)({ root_id: 'r1', root_name: 'Work', local_path: 'C:\\Work' }))

    await waitFor(() => expect(useAppStore.getState().syncRoots).toEqual([{ id: 'r1' }]))
    expect(consoleInfo).toHaveBeenCalled()
    consoleInfo.mockRestore()
  })

  it('refreshes sync roots when a root was renamed', async () => {
    await renderLoggedIn()
    await waitFor(() => expect(renamedListener).toBeDefined())

    listSyncRoots.mockResolvedValue([{ id: 'r2', name: 'Renamed' }])
    act(() => (renamedListener as () => void)())

    await waitFor(() => expect(useAppStore.getState().syncRoots).toEqual([{ id: 'r2', name: 'Renamed' }]))
  })

  it('calls every registered unlisten function on unmount', async () => {
    const { unmount } = renderWithI18n(<App />)
    await screen.findByText('setup-page')

    unmount()

    await waitFor(() => {
      for (const fn of Object.values(unlistenFns)) expect(fn).toHaveBeenCalled()
    })
  })

  describe('bulk-delete warning modal', () => {
    async function openWarning(direction: 'incoming' | 'outgoing' = 'outgoing') {
      await renderLoggedIn()
      await waitFor(() => expect(bulkDeleteListener).toBeDefined())
      act(() => (bulkDeleteListener as (w: unknown) => void)({
        root_id: 'r1', root_name: 'Work', delete_count: 8, total_count: 10, direction,
      }))
      await screen.findByText('Massenlöschung erkannt')
    }

    it('shows the modal with the affected root name', async () => {
      await openWarning()
      expect(screen.getByText(/Work/)).toBeInTheDocument()
    })

    // A percentage computed from delete_count/total_count is misleading once
    // the guard can also trigger on an absolute count on a huge root
    // (ADR-0064): 5000-of-2000000 reads as "0%" even though the fired guard
    // means real, non-trivial data loss. The summary shows raw counts only.
    it('shows raw counts without a computed percentage', async () => {
      await openWarning()
      expect(screen.getByText(/8 von 10 Dateien/)).toBeInTheDocument()
      expect(screen.queryByText(/%/)).not.toBeInTheDocument()
    })

    it('confirms the bulk delete and refreshes sync roots', async () => {
      await openWarning()
      listSyncRoots.mockResolvedValue([{ id: 'r1' }])

      fireEvent.click(screen.getByText(/Dateien endgültig löschen/))

      await waitFor(() => expect(confirmBulkDelete).toHaveBeenCalledWith('r1', 'outgoing'))
      await waitFor(() => expect(useAppStore.getState().syncRoots).toEqual([{ id: 'r1' }]))
      await waitFor(() => expect(screen.queryByText('Massenlöschung erkannt')).not.toBeInTheDocument())
    })

    it('logs and still closes the modal when confirming fails', async () => {
      const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
      await openWarning()
      confirmBulkDelete.mockRejectedValue(new Error('boom'))

      fireEvent.click(screen.getByText(/Dateien endgültig löschen/))

      await waitFor(() => expect(screen.queryByText('Massenlöschung erkannt')).not.toBeInTheDocument())
      expect(consoleError).toHaveBeenCalled()
      consoleError.mockRestore()
    })

    it('restores local files and refreshes sync roots', async () => {
      await openWarning()
      listSyncRoots.mockResolvedValue([{ id: 'r1' }])

      fireEvent.click(screen.getByText('Lokal wiederherstellen'))

      await waitFor(() => expect(restoreLocalFiles).toHaveBeenCalledWith('r1'))
      await waitFor(() => expect(useAppStore.getState().syncRoots).toEqual([{ id: 'r1' }]))
    })

    it('keeps the root paused without calling confirm or restore', async () => {
      await openWarning()
      listSyncRoots.mockResolvedValue([{ id: 'r1' }])

      fireEvent.click(screen.getByText('Pausiert lassen'))

      await waitFor(() => expect(useAppStore.getState().syncRoots).toEqual([{ id: 'r1' }]))
      expect(confirmBulkDelete).not.toHaveBeenCalled()
      expect(restoreLocalFiles).not.toHaveBeenCalled()
      await waitFor(() => expect(screen.queryByText('Massenlöschung erkannt')).not.toBeInTheDocument())
    })

    // Inbound direction: nothing has been removed locally yet, so
    // there is nothing to "restore" -- only apply (proceed) or stay paused.
    it('shows a single apply action for an incoming wave, no restore option', async () => {
      await openWarning('incoming')

      expect(screen.getByText(/Löschungen übernehmen/)).toBeInTheDocument()
      expect(screen.queryByText('Lokal wiederherstellen')).not.toBeInTheDocument()
      expect(screen.queryByText(/Dateien endgültig löschen/)).not.toBeInTheDocument()
    })

    it('confirms an incoming wave with the incoming direction', async () => {
      await openWarning('incoming')
      listSyncRoots.mockResolvedValue([{ id: 'r1' }])

      fireEvent.click(screen.getByText(/Löschungen übernehmen/))

      await waitFor(() => expect(confirmBulkDelete).toHaveBeenCalledWith('r1', 'incoming'))
      await waitFor(() => expect(screen.queryByText('Massenlöschung erkannt')).not.toBeInTheDocument())
    })
  })

  describe('update banner', () => {
    it('shows the banner when an update becomes available and keeps it until installed', async () => {
      await renderLoggedIn()
      await waitFor(() => expect(updateListener).toBeDefined())

      act(() => (updateListener as (info: unknown) => void)({
        version: '1.2.0', current: '1.1.0', notes: null, release_date: null,
      }))

      await screen.findByText(/Update verfügbar: 1\.2\.0/)

      // Not dismissible: no close control, the banner stays.
      expect(screen.queryByTitle('Später')).not.toBeInTheDocument()
      expect(screen.getByText(/Update verfügbar: 1.2.0/)).toBeInTheDocument()
    })

    it('also shows the banner on top of SetupPage, before login', async () => {
      renderWithI18n(<App />)
      await screen.findByText('setup-page')
      await waitFor(() => expect(updateListener).toBeDefined())

      act(() => (updateListener as (info: unknown) => void)({
        version: '1.3.0', current: '1.1.0', notes: null, release_date: null,
      }))

      await screen.findByText(/Update verfügbar: 1\.3\.0/)
      expect(screen.getByText('setup-page')).toBeInTheDocument()
    })
  })
})
