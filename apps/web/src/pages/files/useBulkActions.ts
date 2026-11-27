import { useCallback, useState } from 'react'
import { apiClient } from '../../store/auth'
import { startBrowserDownload } from '../../lib/browserDownload'
import { useTranslation } from '@privasync/i18n'
import type { SyncRoot } from '@privasync/api-client'
import type { UseMultiselect } from './useMultiselect'
import { keyToPath } from './filesViewTypes'

function basename(p: string): string {
  const i = p.lastIndexOf('/')
  return i < 0 ? p : p.slice(i + 1)
}

type Options = {
  selectedRoot: SyncRoot | null
  currentPath:  string
  multi:        UseMultiselect
  refresh:      () => Promise<void> | void
  // When true, selectedRoot.id is actually a share-id and bulk actions
  // route through the Shared-Mount endpoints. See ADR-0010.
  mountMode?:   boolean
  // Sink for move errors raised outside the dialog (drag-and-drop has no
  // dialog to host the inline error).
  onError?:     (msg: string) => void
}

export function useBulkActions({ selectedRoot, currentPath, multi, refresh, mountMode = false, onError }: Options) {
  const { t } = useTranslation()

  const [deleteOpen,   setDeleteOpen]   = useState(false)
  const [deleteLoading, setDeleteLoading] = useState(false)
  const [moveOpen,     setMoveOpen]     = useState(false)
  const [moveLoading,  setMoveLoading]  = useState(false)
  const [moveError,    setMoveError]    = useState<string | null>(null)

  const handleDelete = useCallback(async () => {
    if (!selectedRoot) return
    setDeleteLoading(true)
    try {
      for (const k of Array.from(multi.selected)) {
        const { path } = keyToPath(k)
        try {
          if (mountMode) await apiClient.sharedMounts.delete(selectedRoot.id, path)
          else           await apiClient.files.delete(selectedRoot.id, path)
        }
        catch { /* keep deleting siblings; refresh shows final state */ }
      }
      multi.clear()
      setDeleteOpen(false)
      await refresh()
    } finally {
      setDeleteLoading(false)
    }
  }, [selectedRoot, multi, refresh])

  const handleDownload = useCallback(async () => {
    if (!selectedRoot) return
    for (const k of Array.from(multi.selected)) {
      const { kind, path } = keyToPath(k)
      try {
        const url = mountMode
          ? await apiClient.sharedMounts.downloadUrl(selectedRoot.id, path)
          : await apiClient.files.syncRootDownloadUrl(selectedRoot.id, path)
        startBrowserDownload(url, kind === 'dir' ? basename(path) + '.zip' : basename(path))
      } catch { /* skip failed entries */ }
    }
  }, [selectedRoot, multi.selected])

  // Core mover: relocate `keys` into `targetDir`, routing errors to `sink`.
  // The dialog path sinks into the in-dialog error; drag-and-drop sinks into
  // the page-level error banner via `onError`.
  const runMove = useCallback(async (
    targetDir: string,
    keys:      string[],
    sink:      (msg: string) => void,
  ) => {
    if (!selectedRoot) return
    setMoveLoading(true)
    try {
      const tgt = targetDir.startsWith('/') ? targetDir : '/' + targetDir
      for (const k of keys) {
        const { path } = keyToPath(k)
        const to = (tgt.endsWith('/') ? tgt : tgt + '/') + basename(path)
        if (to === path) continue
        if (mountMode) await apiClient.sharedMounts.move(selectedRoot.id, path, to)
        else           await apiClient.files.move(selectedRoot.id, path, to)
      }
      multi.clear()
      setMoveOpen(false)
      await refresh()
    } catch (err: any) {
      sink(err?.body?.error ?? t('files.bulkMoveError'))
    } finally {
      setMoveLoading(false)
    }
  }, [selectedRoot, mountMode, multi, refresh, t])

  const handleMove = useCallback((targetDir: string) => {
    setMoveError(null)
    return runMove(targetDir, Array.from(multi.selected), setMoveError)
  }, [runMove, multi.selected])

  // Drag-and-drop entry point: move an explicit key set (the dragged rows,
  // which may differ from the current selection).
  const moveTo = useCallback((targetDir: string, keys: Iterable<string>) => {
    return runMove(targetDir, Array.from(keys), (msg) => onError?.(msg))
  }, [runMove, onError])

  return {
    delete:   { open: deleteOpen, loading: deleteLoading, openDialog: () => setDeleteOpen(true),
                closeDialog: () => setDeleteOpen(false), confirm: handleDelete },
    move:     { open: moveOpen,   loading: moveLoading, error: moveError,
                openDialog: () => setMoveOpen(true),
                closeDialog: () => { setMoveOpen(false); setMoveError(null) },
                initialPath: currentPath,
                confirm: handleMove },
    moveTo,
    download: handleDownload,
  }
}
