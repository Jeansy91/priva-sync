import { useCallback } from 'react'
import type { SyncRoot } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { startBrowserDownload } from '../../lib/browserDownload'
import type { DirEntry } from './helpers'

interface UseFilesEntryActionsInput {
  selectedRoot: SyncRoot | null
  currentPath:  string
  mountMode:    boolean
  t: (key: string) => string
  loadFiles:      (rootID: string, path: string) => void | Promise<void>
  bumpTreeReload: () => void
  setError:       (msg: string | null) => void

  deleteTarget:     DirEntry | null
  setDeleteTarget:  (e: DirEntry | null) => void
  setDeleteLoading: (loading: boolean) => void
}

/**
 * Per-entry actions triggered from row context menus / table buttons:
 * download (file or directory subtree) and the single-entry delete that
 * the delete-confirmation dialog flushes.
 */
export function useFilesEntryActions(input: UseFilesEntryActionsInput) {
  const {
    selectedRoot, currentPath, mountMode, t,
    loadFiles, bumpTreeReload, setError,
    deleteTarget, setDeleteTarget, setDeleteLoading,
  } = input

  const handleDownload = useCallback(async (entry: DirEntry) => {
    if (!selectedRoot) return
    try {
      // One-time token URL: the server answers a directory with a ZIP
      // stream and a file with the file itself.
      const path = entry.kind === 'dir' ? entry.path : entry.file.path
      const url = mountMode
        ? await apiClient.sharedMounts.downloadUrl(selectedRoot.id, path)
        : await apiClient.files.syncRootDownloadUrl(selectedRoot.id, path)
      startBrowserDownload(url, entry.kind === 'dir' ? `${entry.name}.zip` : entry.name)
    } catch {
      setError(t('files.downloadFailed'))
    }
  }, [selectedRoot, mountMode, t, setError])

  const handleDelete = useCallback(async () => {
    if (!selectedRoot || !deleteTarget) return
    setDeleteLoading(true)
    try {
      // Server cascades directory deletes via deleteDirectorySubtree.
      const targetPath = deleteTarget.kind === 'file'
        ? deleteTarget.file.path
        : deleteTarget.path
      if (mountMode) {
        await apiClient.sharedMounts.delete(selectedRoot.id, targetPath)
      } else {
        await apiClient.files.delete(selectedRoot.id, targetPath)
      }
      setDeleteTarget(null)
      await loadFiles(selectedRoot.id, currentPath)
      bumpTreeReload()
    } catch {
      // stay open on error
    } finally {
      setDeleteLoading(false)
    }
  }, [
    selectedRoot, deleteTarget, currentPath, mountMode,
    loadFiles, bumpTreeReload, setDeleteTarget, setDeleteLoading,
  ])

  return { handleDownload, handleDelete }
}
