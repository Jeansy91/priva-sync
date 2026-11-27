import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useState } from 'react'
import { renderHook, act } from '@testing-library/react'
import type { SyncRoot } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { useFilesRootActions } from './useFilesRootActions'

const ROOT: SyncRoot = {
  id:         'r-1',
  user_id:    'u-1',
  name:       'Photos',
  path:       'Photos',
  created_at: '2026-01-01T00:00:00Z',
}

// Drives useFilesRootActions with a real syncRoots state container so the
// optimistic append in handleCreateRoot is observable. Everything else is
// stubbed — only the create path matters here.
function useHarness(initial: SyncRoot[]) {
  const [syncRoots, setSyncRoots] = useState<SyncRoot[]>(initial)
  const actions = useFilesRootActions({
    selectedRoot:         null,
    setSelectedRoot:      vi.fn(),
    setSyncRoots,
    setError:             vi.fn(),
    t:                    (k) => k,
    go:                   vi.fn() as never,
    newRootName:          'Photos',
    setNewRootName:       vi.fn(),
    setCreateRootOpen:    vi.fn(),
    setCreateRootLoading: vi.fn(),
    setCreateRootError:   vi.fn(),
    deleteRootTarget:     null,
    deleteRootConfirm:    '',
    setDeleteRootTarget:  vi.fn(),
    setDeleteRootConfirm: vi.fn(),
    setDeleteRootLoading: vi.fn(),
    setDownloadingRoot:   vi.fn(),
  })
  return { syncRoots, ...actions }
}

beforeEach(() => { vi.restoreAllMocks() })

describe('useFilesRootActions — handleCreateRoot', () => {
  it('does not duplicate when the SSE event already inserted the root', async () => {
    // The sync_root_added SSE handler can win the race against the HTTP
    // response and insert the root first. The optimistic append must then
    // be a no-op instead of producing a second copy.
    vi.spyOn(apiClient.files, 'createSyncRoot').mockResolvedValue(ROOT as never)
    const { result } = renderHook(() => useHarness([ROOT]))

    await act(async () => { await result.current.handleCreateRoot() })

    expect(result.current.syncRoots.filter((r) => r.id === 'r-1')).toHaveLength(1)
  })

  it('appends the new root when it is not present yet', async () => {
    vi.spyOn(apiClient.files, 'createSyncRoot').mockResolvedValue(ROOT as never)
    const { result } = renderHook(() => useHarness([]))

    await act(async () => { await result.current.handleCreateRoot() })

    expect(result.current.syncRoots).toEqual([ROOT])
  })

  it('no-ops on a blank name', async () => {
    const createSpy = vi.spyOn(apiClient.files, 'createSyncRoot')
    const { result } = renderHook(() => useFilesRootActions(baseInput({ newRootName: '   ' })))

    await act(async () => { await result.current.handleCreateRoot() })

    expect(createSpy).not.toHaveBeenCalled()
  })

  it('reports the server error message on failure', async () => {
    vi.spyOn(apiClient.files, 'createSyncRoot').mockRejectedValue({ body: { error: 'name taken' } })
    const setCreateRootError = vi.fn()
    const { result } = renderHook(() => useFilesRootActions(baseInput({
      newRootName: 'Videos', setCreateRootError,
    })))

    await act(async () => { await result.current.handleCreateRoot() })

    expect(setCreateRootError).toHaveBeenCalledWith('name taken')
  })
})

function spyOnAppendChild() {
  let captured: HTMLAnchorElement | undefined
  const clickSpy = vi.fn()
  vi.spyOn(document.body, 'appendChild').mockImplementation((node) => {
    captured = node as HTMLAnchorElement
    Object.defineProperty(captured, 'click', { value: clickSpy, configurable: true })
    return node
  })
  vi.spyOn(document.body, 'removeChild').mockImplementation((node) => node)
  return { getAnchor: () => captured, clickSpy }
}

function baseInput(over: Partial<Parameters<typeof useFilesRootActions>[0]> = {}) {
  return {
    selectedRoot: null,
    setSelectedRoot: vi.fn(),
    setSyncRoots: vi.fn(),
    setError: vi.fn(),
    t: (k: string) => k,
    go: vi.fn() as never,
    newRootName: '',
    setNewRootName: vi.fn(),
    setCreateRootOpen: vi.fn(),
    setCreateRootLoading: vi.fn(),
    setCreateRootError: vi.fn(),
    deleteRootTarget: null,
    deleteRootConfirm: '',
    setDeleteRootTarget: vi.fn(),
    setDeleteRootConfirm: vi.fn(),
    setDeleteRootLoading: vi.fn(),
    setDownloadingRoot: vi.fn(),
    ...over,
  }
}

describe('useFilesRootActions — downloadRootSubtree', () => {
  it('names the zip after the subtree basename', async () => {
    vi.spyOn(apiClient.files, 'syncRootDownloadUrl').mockResolvedValue('http://x/dl')
    const { getAnchor, clickSpy } = spyOnAppendChild()
    const { result } = renderHook(() => useFilesRootActions(baseInput()))

    await act(async () => { await result.current.downloadRootSubtree(ROOT, '/Reports/2026') })

    expect(getAnchor()?.download).toBe('2026.zip')
    expect(clickSpy).toHaveBeenCalled()
  })

  it('names the zip after the root when the subtree path is the root', async () => {
    vi.spyOn(apiClient.files, 'syncRootDownloadUrl').mockResolvedValue('http://x/dl')
    const { getAnchor } = spyOnAppendChild()
    const { result } = renderHook(() => useFilesRootActions(baseInput()))

    await act(async () => { await result.current.downloadRootSubtree(ROOT, '/') })

    expect(getAnchor()?.download).toBe('Photos.zip')
  })

  it('reports an error when the download URL request fails', async () => {
    vi.spyOn(apiClient.files, 'syncRootDownloadUrl').mockRejectedValue(new Error('fail'))
    const setError = vi.fn()
    const { result } = renderHook(() => useFilesRootActions(baseInput({ setError })))

    await act(async () => { await result.current.downloadRootSubtree(ROOT, '/x') })

    expect(setError).toHaveBeenCalledWith('files.downloadFailed')
  })
})

describe('useFilesRootActions — downloadRoot', () => {
  it('tracks downloading state and clicks the anchor', async () => {
    vi.spyOn(apiClient.files, 'syncRootDownloadUrl').mockResolvedValue('http://x/dl')
    const { getAnchor, clickSpy } = spyOnAppendChild()
    const setDownloadingRoot = vi.fn()
    const { result } = renderHook(() => useFilesRootActions(baseInput({ setDownloadingRoot })))

    await act(async () => { await result.current.downloadRoot(ROOT) })

    expect(setDownloadingRoot).toHaveBeenCalledWith('r-1')
    expect(setDownloadingRoot).toHaveBeenLastCalledWith(null)
    expect(getAnchor()?.download).toBe('Photos.zip')
    expect(clickSpy).toHaveBeenCalled()
  })

  it('reports an error and clears downloading state on failure', async () => {
    vi.spyOn(apiClient.files, 'syncRootDownloadUrl').mockRejectedValue(new Error('fail'))
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const setError = vi.fn()
    const setDownloadingRoot = vi.fn()
    const { result } = renderHook(() => useFilesRootActions(baseInput({ setError, setDownloadingRoot })))

    await act(async () => { await result.current.downloadRoot(ROOT) })

    expect(setError).toHaveBeenCalledWith('files.downloadFailed')
    expect(setDownloadingRoot).toHaveBeenLastCalledWith(null)
  })
})

describe('useFilesRootActions — handleDeleteRoot', () => {
  it('no-ops when the confirmation text does not match', async () => {
    const deleteSpy = vi.spyOn(apiClient.files, 'deleteSyncRoot')
    const { result } = renderHook(() => useFilesRootActions(baseInput({
      deleteRootTarget: ROOT, deleteRootConfirm: 'wrong',
    })))

    await act(async () => { await result.current.handleDeleteRoot() })

    expect(deleteSpy).not.toHaveBeenCalled()
  })

  it('deletes, navigates away and clears state when the open root is deleted', async () => {
    vi.spyOn(apiClient.files, 'deleteSyncRoot').mockResolvedValue(undefined as never)
    const setSelectedRoot = vi.fn()
    const setSyncRoots = vi.fn()
    const setDeleteRootTarget = vi.fn()
    const setDeleteRootConfirm = vi.fn()
    const go = vi.fn()
    const { result } = renderHook(() => useFilesRootActions(baseInput({
      selectedRoot: ROOT, deleteRootTarget: ROOT, deleteRootConfirm: 'Photos',
      setSelectedRoot, setSyncRoots, setDeleteRootTarget, setDeleteRootConfirm, go: go as never,
    })))

    await act(async () => { await result.current.handleDeleteRoot() })

    expect(setSelectedRoot).toHaveBeenCalledWith(null)
    expect(go).toHaveBeenCalledWith('/files')
    expect(setDeleteRootTarget).toHaveBeenCalledWith(null)
    expect(setDeleteRootConfirm).toHaveBeenCalledWith('')
    expect(setSyncRoots).toHaveBeenCalled()
  })

  it('deletes without navigating when a different root is open', async () => {
    vi.spyOn(apiClient.files, 'deleteSyncRoot').mockResolvedValue(undefined as never)
    const go = vi.fn()
    const { result } = renderHook(() => useFilesRootActions(baseInput({
      selectedRoot: { ...ROOT, id: 'other' }, deleteRootTarget: ROOT, deleteRootConfirm: 'Photos',
      go: go as never,
    })))

    await act(async () => { await result.current.handleDeleteRoot() })

    expect(go).not.toHaveBeenCalled()
  })

  it('reports the server error message on failure', async () => {
    vi.spyOn(apiClient.files, 'deleteSyncRoot').mockRejectedValue({ body: { error: 'in use' } })
    const setError = vi.fn()
    const { result } = renderHook(() => useFilesRootActions(baseInput({
      deleteRootTarget: ROOT, deleteRootConfirm: ROOT.name, setError,
    })))

    await act(async () => { await result.current.handleDeleteRoot() })

    expect(setError).toHaveBeenCalledWith('in use')
  })
})
