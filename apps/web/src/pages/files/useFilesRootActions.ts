import { useCallback } from 'react'
import type { NavigateFunction } from 'react-router-dom'
import type { SyncRoot } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { startBrowserDownload } from '../../lib/browserDownload'
import { basename } from './filesPagePrefs'

interface UseFilesRootActionsInput {
  selectedRoot:    SyncRoot | null
  setSelectedRoot: (r: SyncRoot | null) => void
  setSyncRoots:    React.Dispatch<React.SetStateAction<SyncRoot[]>>
  setError:        (msg: string | null) => void
  t:               (key: string) => string
  go:              NavigateFunction

  // Create-root dialog state owned by useFilesPageDialogs.
  newRootName:          string
  setNewRootName:       (s: string) => void
  setCreateRootOpen:    (open: boolean) => void
  setCreateRootLoading: (loading: boolean) => void
  setCreateRootError:   (msg: string | null) => void

  // Delete-root dialog state.
  deleteRootTarget:     SyncRoot | null
  deleteRootConfirm:    string
  setDeleteRootTarget:  (t: SyncRoot | null) => void
  setDeleteRootConfirm: (s: string) => void
  setDeleteRootLoading: (loading: boolean) => void

  // Download progress flag.
  setDownloadingRoot: (id: string | null) => void
}

/**
 * Bundles the sync-root level actions: create, delete (with confirmation),
 * ZIP download of a whole root, and ZIP download of a subtree path.
 *
 * Lives outside the page so the JSX only sees the four callbacks — the
 * underlying dialog state plumbing stays in useFilesPageDialogs.
 */
export function useFilesRootActions(input: UseFilesRootActionsInput) {
  const {
    selectedRoot, setSelectedRoot, setSyncRoots, setError, t, go,
    newRootName, setNewRootName, setCreateRootOpen, setCreateRootLoading, setCreateRootError,
    deleteRootTarget, deleteRootConfirm, setDeleteRootTarget, setDeleteRootConfirm, setDeleteRootLoading,
    setDownloadingRoot,
  } = input

  const downloadRootSubtree = useCallback(async (root: SyncRoot, subPath: string) => {
    try {
      const url = await apiClient.files.syncRootDownloadUrl(root.id, subPath)
      startBrowserDownload(url, (subPath === '/' || subPath === '' ? root.name : basename(subPath)) + '.zip')
    } catch {
      setError(t('files.downloadFailed'))
    }
  }, [t, setError])

  const downloadRoot = useCallback(async (root: SyncRoot) => {
    setDownloadingRoot(root.id)
    try {
      // Short-lived token + window-driven navigation: the browser streams the
      // ZIP straight to the save-dialog, no RAM buffering — works for >1 GB.
      const url = await apiClient.files.syncRootDownloadUrl(root.id)
      startBrowserDownload(url, `${root.name}.zip`)
    } catch (err) {
      console.error('download sync root failed', err)
      setError(t('files.downloadFailed'))
    } finally {
      setDownloadingRoot(null)
    }
  }, [t, setError, setDownloadingRoot])

  const handleDeleteRoot = useCallback(async () => {
    if (!deleteRootTarget || deleteRootConfirm !== deleteRootTarget.name) return
    setDeleteRootLoading(true)
    try {
      await apiClient.files.deleteSyncRoot(deleteRootTarget.id)
      // If the currently-open root was deleted -> back to the root list.
      if (selectedRoot?.id === deleteRootTarget.id) {
        setSelectedRoot(null)
        go('/files')
      }
      setSyncRoots((prev) => prev.filter((r) => r.id !== deleteRootTarget.id))
      setDeleteRootTarget(null)
      setDeleteRootConfirm('')
    } catch (err: unknown) {
      const msg = (err as { body?: { error?: string } } | null)?.body?.error
      setError(msg ?? t('files.deleteRootFailed'))
    } finally {
      setDeleteRootLoading(false)
    }
  }, [
    deleteRootTarget, deleteRootConfirm, selectedRoot, go, t,
    setSelectedRoot, setSyncRoots, setDeleteRootTarget, setDeleteRootConfirm,
    setDeleteRootLoading, setError,
  ])

  const handleCreateRoot = useCallback(async () => {
    if (!newRootName.trim()) return
    setCreateRootLoading(true)
    setCreateRootError(null)
    try {
      const root = await apiClient.files.createSyncRoot(newRootName.trim())
      // Guard against the sync_root_added SSE event winning the race and
      // inserting this root first — append only when it is not present yet,
      // mirroring the dedup in useFilesSSE so the order never matters.
      setSyncRoots((prev) => (prev.some((r) => r.id === root.id) ? prev : [...prev, root]))
      // Stay at root level so the user sees the new folder appear.
      setSelectedRoot(null)
      setCreateRootOpen(false)
      setNewRootName('')
    } catch (err: unknown) {
      const msg = (err as { body?: { error?: string } } | null)?.body?.error
      setCreateRootError(msg ?? t('files.createRootError'))
    } finally {
      setCreateRootLoading(false)
    }
  }, [
    newRootName, t,
    setSyncRoots, setSelectedRoot, setCreateRootOpen, setNewRootName,
    setCreateRootLoading, setCreateRootError,
  ])

  return { downloadRootSubtree, downloadRoot, handleDeleteRoot, handleCreateRoot }
}
