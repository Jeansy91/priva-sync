import { useState } from 'react'
import type { SyncRoot } from '@privasync/api-client'
import type { DirEntry } from './helpers'
import type { ContextMenuAnchor } from './EntryContextMenu'
import type { EntryMenuCallbacks } from './entryMenuItems'

export interface CtxMenuState {
  entry:     DirEntry | null
  anchor:    ContextMenuAnchor
  callbacks: EntryMenuCallbacks
}

/**
 * Bundles the modal/dialog state of FilesPage. Each dialog has at most a few
 * useState slots — together they fill up the page with cross-cutting noise,
 * so we keep them in one hook.
 *
 * Only purely-modal state lives here. State that participates in effects
 * (pendingAction, selectedSyncRootID) stays in the page.
 */
export function useFilesPageDialogs() {
  // Create sync root
  const [createRootOpen, setCreateRootOpen]       = useState(false)
  const [newRootName, setNewRootName]             = useState('')
  const [createRootLoading, setCreateRootLoading] = useState(false)
  const [createRootError, setCreateRootError]     = useState<string | null>(null)

  // Delete single entry
  const [deleteTarget, setDeleteTarget]   = useState<DirEntry | null>(null)
  const [deleteLoading, setDeleteLoading] = useState(false)

  // Entry context menu
  const [ctxMenu, setCtxMenu] = useState<CtxMenuState | null>(null)

  // Delete entire sync root
  const [deleteRootTarget, setDeleteRootTarget]   = useState<SyncRoot | null>(null)
  const [deleteRootConfirm, setDeleteRootConfirm] = useState('')
  const [deleteRootLoading, setDeleteRootLoading] = useState(false)
  const [downloadingRoot, setDownloadingRoot]     = useState<string | null>(null)

  return {
    createRootOpen, setCreateRootOpen,
    newRootName, setNewRootName,
    createRootLoading, setCreateRootLoading,
    createRootError, setCreateRootError,

    deleteTarget, setDeleteTarget,
    deleteLoading, setDeleteLoading,

    ctxMenu, setCtxMenu,

    deleteRootTarget, setDeleteRootTarget,
    deleteRootConfirm, setDeleteRootConfirm,
    deleteRootLoading, setDeleteRootLoading,
    downloadingRoot, setDownloadingRoot,
  }
}
