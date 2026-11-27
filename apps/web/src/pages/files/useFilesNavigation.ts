import { useCallback } from 'react'
import type { NavigateFunction } from 'react-router-dom'
import type { SyncRoot } from '@privasync/api-client'
import { toFilesUrl, toMountUrl } from './helpers'
import type { UploadSplitButtonHandle } from './UploadSplitButton'

export interface PendingAction {
  kind:   'newFolder' | 'upload'
  rootID: string
  path:   string
}

interface UseFilesNavigationInput {
  go:              NavigateFunction
  selectedRoot:    SyncRoot | null
  currentPath:     string
  mountMode:       boolean
  createFolderOpenFor: (path: string) => void
  uploadBtnRef:    React.RefObject<UploadSplitButtonHandle | null>
  setPendingAction: (action: PendingAction | null) => void
}

/**
 * Route-changing callbacks: entering/leaving a sync root, jumping to the
 * Shares page for a path, and the two "act here, or navigate there first"
 * triggers for empty-area context menus (new folder / upload targeting a
 * sibling directory that isn't the current one).
 *
 * triggerNewFolder/triggerUpload act immediately when the target is already
 * open; otherwise they stash a PendingAction and navigate — FilesPage fires
 * it once the URL-driven state settles onto that path (see the
 * pendingAction effect in FilesPage).
 */
export function useFilesNavigation({
  go, selectedRoot, currentPath, mountMode, createFolderOpenFor, uploadBtnRef, setPendingAction,
}: UseFilesNavigationInput) {
  const navigateToRoot = useCallback(() => { go('/files') }, [go])

  // Sharing is configured on the Shares page, not in a modal: navigate there
  // with the path preselected so the (possibly new) share opens in edit mode.
  const goShare = useCallback((rootID: string, path: string, isDir: boolean) => {
    const q = new URLSearchParams({ root: rootID, path })
    if (isDir) q.set('dir', '1')
    go(`/shares?${q.toString()}`)
  }, [go])

  const navigate = useCallback((path: string) => {
    if (!selectedRoot) return
    const normalised = path.startsWith('/') ? path : '/' + path
    go(mountMode
      ? toMountUrl(selectedRoot.id, normalised)
      : toFilesUrl(selectedRoot.name, normalised))
  }, [go, selectedRoot, mountMode])

  const enterSyncRoot = useCallback((root: SyncRoot) => {
    go(toFilesUrl(root.name))
  }, [go])

  const triggerNewFolder = useCallback((root: SyncRoot, path: string) => {
    if (selectedRoot?.id === root.id && currentPath === path) {
      createFolderOpenFor(path)
      return
    }
    setPendingAction({ kind: 'newFolder', rootID: root.id, path })
    go(toFilesUrl(root.name, path))
  }, [selectedRoot, currentPath, createFolderOpenFor, setPendingAction, go])

  const triggerUpload = useCallback((root: SyncRoot, path: string) => {
    if (selectedRoot?.id === root.id && currentPath === path) {
      uploadBtnRef.current?.openFilePicker()
      return
    }
    setPendingAction({ kind: 'upload', rootID: root.id, path })
    go(toFilesUrl(root.name, path))
  }, [selectedRoot, currentPath, uploadBtnRef, setPendingAction, go])

  return { navigateToRoot, goShare, navigate, enterSyncRoot, triggerNewFolder, triggerUpload }
}
