import { describe, it, expect, vi, beforeEach } from 'vitest'

const invoke = vi.fn().mockResolvedValue(undefined)
const listen = vi.fn().mockResolvedValue(() => {})

vi.mock('@tauri-apps/api/core', () => ({ invoke: (...a: unknown[]) => invoke(...a) }))
vi.mock('@tauri-apps/api/event', () => ({ listen: (...a: unknown[]) => listen(...a) }))

import { cmd, onSyncStatusChanged, onSyncConflict, onSessionExpired, onSyncProgress,
  onBulkDeleteWarning, onPathWarning, onRootOrphaned, onRootRenamed, onSharesChanged,
  onSyncFailure, onUpdateAvailable, onTransferSpeed, onTrayNavigate } from './tauri'

beforeEach(() => {
  invoke.mockClear()
  listen.mockClear()
})

// Every entry in cmd.* wraps invoke() with a fixed command name and argument
// shape (mirrors the Rust #[tauri::command] signatures) — verify the mapping
// stays intact rather than testing invoke() itself.
describe('cmd', () => {
  it('wraps no-arg commands with just the command name', () => {
    cmd.getSyncStatus()
    expect(invoke).toHaveBeenCalledWith('get_sync_status')

    cmd.getConflicts()
    expect(invoke).toHaveBeenCalledWith('get_conflicts')

    cmd.getConfig()
    expect(invoke).toHaveBeenCalledWith('get_config')

    cmd.logout()
    expect(invoke).toHaveBeenCalledWith('logout')

    cmd.fetchMyAvatar()
    expect(invoke).toHaveBeenCalledWith('fetch_my_avatar')

    cmd.wipeAccountLocal()
    expect(invoke).toHaveBeenCalledWith('wipe_account_local')

    cmd.listServerSyncRoots()
    expect(invoke).toHaveBeenCalledWith('list_server_sync_roots')

    cmd.listSyncRoots()
    expect(invoke).toHaveBeenCalledWith('list_sync_roots')

    cmd.getIgnorePatterns()
    expect(invoke).toHaveBeenCalledWith('get_ignore_patterns')

    cmd.getDefaultIgnorePatterns()
    expect(invoke).toHaveBeenCalledWith('get_default_ignore_patterns')

    cmd.countExcludedFiles()
    expect(invoke).toHaveBeenCalledWith('count_excluded_files')

    cmd.forceSync()
    expect(invoke).toHaveBeenCalledWith('force_sync')

    cmd.listSyncFailures()
    expect(invoke).toHaveBeenCalledWith('list_sync_failures')

    cmd.getUploadConcurrency()
    expect(invoke).toHaveBeenCalledWith('get_upload_concurrency')

    cmd.getAutostart()
    expect(invoke).toHaveBeenCalledWith('get_autostart')

    cmd.getLanguage()
    expect(invoke).toHaveBeenCalledWith('get_language')

    cmd.installUpdate()
    expect(invoke).toHaveBeenCalledWith('install_update')
  })

  it('wraps checkForUpdate with an optional server-URL override', () => {
    cmd.checkForUpdate()
    expect(invoke).toHaveBeenCalledWith('check_for_update', { serverUrl: undefined })

    cmd.checkForUpdate('https://sync.example.com')
    expect(invoke).toHaveBeenCalledWith('check_for_update', { serverUrl: 'https://sync.example.com' })
  })

  it('wraps the startup auto-update bindings', () => {
    cmd.getUpdateStage()
    expect(invoke).toHaveBeenCalledWith('get_update_stage')

    cmd.startAutoUpdateFor('https://sync.example.com')
    expect(invoke).toHaveBeenCalledWith('start_auto_update_for', { serverUrl: 'https://sync.example.com' })
  })

  it('wraps verifyInstance with the candidate URL', () => {
    cmd.verifyInstance('https://sync.example.com')
    expect(invoke).toHaveBeenCalledWith('verify_instance', { url: 'https://sync.example.com' })
  })

  it('forwards login credentials with their Rust parameter names', () => {
    cmd.login('http://x', 'alice', 'secret')
    expect(invoke).toHaveBeenCalledWith('login', { serverUrl: 'http://x', username: 'alice', password: 'secret' })
  })

  it('wraps sync-root commands with their arguments', () => {
    cmd.createServerSyncRoot('Work')
    expect(invoke).toHaveBeenCalledWith('create_server_sync_root', { name: 'Work' })

    cmd.addSyncRoot('id1', 'Work', 'C:\\Work', 'srv1', 'eager')
    expect(invoke).toHaveBeenCalledWith('add_sync_root', {
      id: 'id1', name: 'Work', localPath: 'C:\\Work', serverRootId: 'srv1', downloadMode: 'eager',
    })

    cmd.removeSyncRoot('id1')
    expect(invoke).toHaveBeenCalledWith('remove_sync_root', { id: 'id1' })

    cmd.getRemoveSyncRootOptions('id1')
    expect(invoke).toHaveBeenCalledWith('get_remove_sync_root_options', { id: 'id1' })

    cmd.setDownloadMode('id1', 'on_demand')
    expect(invoke).toHaveBeenCalledWith('set_download_mode', { id: 'id1', mode: 'on_demand' })
  })

  it('wraps shared-mount commands with their arguments', () => {
    cmd.listAvailableMounts()
    expect(invoke).toHaveBeenCalledWith('list_available_mounts')

    cmd.activateMount('share1', 'Team', 'C:\\Team', true, 'bob')
    expect(invoke).toHaveBeenCalledWith('activate_mount', {
      shareId: 'share1', name: 'Team', localPath: 'C:\\Team', canWrite: true, ownerUsername: 'bob',
    })

    cmd.removeMount('share1')
    expect(invoke).toHaveBeenCalledWith('remove_mount', { shareId: 'share1' })
  })

  it('wraps exclusion-pattern commands with their arguments', () => {
    cmd.validateIgnorePatterns('*.tmp')
    expect(invoke).toHaveBeenCalledWith('validate_ignore_patterns', { patterns: '*.tmp' })

    cmd.setIgnorePatterns('*.tmp\n*.log')
    expect(invoke).toHaveBeenCalledWith('set_ignore_patterns', { patterns: '*.tmp\n*.log' })
  })

  it('wraps sync-lifecycle and conflict commands with their arguments', () => {
    cmd.confirmBulkDelete('r1', 'incoming')
    expect(invoke).toHaveBeenCalledWith('confirm_bulk_delete', { rootId: 'r1', direction: 'incoming' })

    cmd.pauseSyncRoot('r1')
    expect(invoke).toHaveBeenCalledWith('pause_sync_root', { rootId: 'r1' })

    cmd.resumeSyncRoot('r1')
    expect(invoke).toHaveBeenCalledWith('resume_sync_root', { rootId: 'r1' })

    cmd.restoreLocalFiles('r1')
    expect(invoke).toHaveBeenCalledWith('restore_local_files', { rootId: 'r1' })

    cmd.resolveConflict('r1', '/a.txt', 'keep_current')
    expect(invoke).toHaveBeenCalledWith('resolve_conflict', { rootId: 'r1', path: '/a.txt', strategy: 'keep_current' })

    cmd.conflictDiff('r1', '/a.txt')
    expect(invoke).toHaveBeenCalledWith('conflict_diff', { rootId: 'r1', path: '/a.txt' })
  })

  it('wraps retry, concurrency, autostart and language setters', () => {
    cmd.retrySyncFailure('upload', 'r1', '/a.txt')
    expect(invoke).toHaveBeenCalledWith('retry_sync_failure', { kind: 'upload', rootId: 'r1', path: '/a.txt' })

    cmd.setUploadConcurrency(4)
    expect(invoke).toHaveBeenCalledWith('set_upload_concurrency', { value: 4 })

    cmd.setAutostart(true)
    expect(invoke).toHaveBeenCalledWith('set_autostart', { enabled: true })

    cmd.setLanguage('de')
    expect(invoke).toHaveBeenCalledWith('set_language', { lang: 'de' })
  })
})

// Every on*() wraps listen() with a fixed event name and unwraps e.payload
// into the callback (or calls it with no args for payload-less events).
describe('event subscriptions', () => {
  it('unwraps the payload for status/conflict/progress-style events', () => {
    const status = vi.fn()
    onSyncStatusChanged(status)
    expect(listen).toHaveBeenCalledWith('sync_status_changed', expect.any(Function))
    listen.mock.calls[listen.mock.calls.length - 1][1]({ payload: { status: 'idle' } })
    expect(status).toHaveBeenCalledWith({ status: 'idle' })

    const conflict = vi.fn()
    onSyncConflict(conflict)
    expect(listen).toHaveBeenCalledWith('sync_conflict', expect.any(Function))
    listen.mock.calls[listen.mock.calls.length - 1][1]({ payload: { path: '/a' } })
    expect(conflict).toHaveBeenCalledWith({ path: '/a' })

    const progress = vi.fn()
    onSyncProgress(progress)
    listen.mock.calls[listen.mock.calls.length - 1][1]({ payload: { root_id: 'r1' } })
    expect(progress).toHaveBeenCalledWith({ root_id: 'r1' })

    const bulkDelete = vi.fn()
    onBulkDeleteWarning(bulkDelete)
    expect(listen).toHaveBeenCalledWith('sync:bulk_delete_warning', expect.any(Function))
    listen.mock.calls[listen.mock.calls.length - 1][1]({ payload: { root_id: 'r1' } })
    expect(bulkDelete).toHaveBeenCalledWith({ root_id: 'r1' })

    const pathWarning = vi.fn()
    onPathWarning(pathWarning)
    expect(listen).toHaveBeenCalledWith('sync:path_warning', expect.any(Function))
    listen.mock.calls[listen.mock.calls.length - 1][1]({ payload: { path: '/a', code: 'x' } })
    expect(pathWarning).toHaveBeenCalledWith({ path: '/a', code: 'x' })

    const orphaned = vi.fn()
    onRootOrphaned(orphaned)
    expect(listen).toHaveBeenCalledWith('sync:root_orphaned', expect.any(Function))
    listen.mock.calls[listen.mock.calls.length - 1][1]({ payload: { root_id: 'r1' } })
    expect(orphaned).toHaveBeenCalledWith({ root_id: 'r1' })

    const failure = vi.fn()
    onSyncFailure(failure)
    expect(listen).toHaveBeenCalledWith('sync_failure', expect.any(Function))
    listen.mock.calls[listen.mock.calls.length - 1][1]({ payload: { kind: 'upload' } })
    expect(failure).toHaveBeenCalledWith({ kind: 'upload' })

    const update = vi.fn()
    onUpdateAvailable(update)
    expect(listen).toHaveBeenCalledWith('update_available', expect.any(Function))
    listen.mock.calls[listen.mock.calls.length - 1][1]({ payload: { version: '1.0' } })
    expect(update).toHaveBeenCalledWith({ version: '1.0' })

    const renamed = vi.fn()
    onRootRenamed(renamed)
    expect(listen).toHaveBeenCalledWith('sync:root_renamed', expect.any(Function))
    listen.mock.calls[listen.mock.calls.length - 1][1]({ payload: { root_id: 'r1', name: 'New' } })
    expect(renamed).toHaveBeenCalledWith({ root_id: 'r1', name: 'New' })

    const speed = vi.fn()
    onTransferSpeed(speed)
    expect(listen).toHaveBeenCalledWith('transfer_speed', expect.any(Function))
    listen.mock.calls[listen.mock.calls.length - 1][1]({ payload: { upload_bps: 1 } })
    expect(speed).toHaveBeenCalledWith({ upload_bps: 1 })

    const navigate = vi.fn()
    onTrayNavigate(navigate)
    expect(listen).toHaveBeenCalledWith('tray_navigate', expect.any(Function))
    listen.mock.calls[listen.mock.calls.length - 1][1]({ payload: 'settings' })
    expect(navigate).toHaveBeenCalledWith('settings')
  })

  it('drops a tray_navigate payload that names no page', () => {
    const navigate = vi.fn()
    onTrayNavigate(navigate)
    listen.mock.calls[listen.mock.calls.length - 1][1]({ payload: 'nowhere' })
    expect(navigate).not.toHaveBeenCalled()
  })

  it('calls payload-less callbacks with no arguments', () => {
    const session = vi.fn()
    onSessionExpired(session)
    expect(listen).toHaveBeenCalledWith('session_expired', expect.any(Function))
    listen.mock.calls[listen.mock.calls.length - 1][1]()
    expect(session).toHaveBeenCalledWith()

    const shares = vi.fn()
    onSharesChanged(shares)
    expect(listen).toHaveBeenCalledWith('shares_changed', expect.any(Function))
    listen.mock.calls[listen.mock.calls.length - 1][1]()
    expect(shares).toHaveBeenCalledWith()
  })
})
