import type { NavigateFunction } from 'react-router-dom'
import type { SyncRoot, SharedMount, FileEntry, PathShareInfo } from '@privasync/api-client'
import { typed } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { useSSE } from '../../hooks/useSSE'
import { invalidateThumb } from './thumbCache'
import { isAncestorOrEqual, parentPath, toFilesUrl } from './helpers'

interface UseFilesSSEInput {
  selectedRoot: SyncRoot | null
  setSelectedRoot: (r: SyncRoot | null) => void
  currentPath:  string
  allFiles:     FileEntry[]
  mountMode:    boolean
  mountIDParam: string | null
  syncRoots:    SyncRoot[]
  setSyncRoots:    React.Dispatch<React.SetStateAction<SyncRoot[]>>
  setMounts:       React.Dispatch<React.SetStateAction<SharedMount[]>>
  setSharesByPath: React.Dispatch<React.SetStateAction<Record<string, PathShareInfo[]>>>
  bumpTreeReload:    () => void
  refreshRootShares: (roots: SyncRoot[]) => Promise<void> | void
  loadFiles: (rootID: string, path: string) => void
  loadRoots: () => void
  go: NavigateFunction
}

/**
 * Wires the SSE event handlers for FilesPage: file mutations refetch the
 * active listing, sync-root events patch the inventory, share events
 * refresh the relevant badge maps and mount list.
 *
 * On reconnect / tab focus we refetch the whole top-level inventory plus
 * the current listing, because intermediate events may have been missed.
 */
export function useFilesSSE(input: UseFilesSSEInput): void {
  const {
    selectedRoot, setSelectedRoot, currentPath, allFiles,
    mountMode, mountIDParam, syncRoots,
    setSyncRoots, setMounts, setSharesByPath,
    bumpTreeReload, refreshRootShares,
    loadFiles, loadRoots, go,
  } = input

  useSSE({
    file_uploaded: typed<'file_uploaded'>(({ root_id, file }) => {
      if (!selectedRoot || root_id !== selectedRoot.id) return
      invalidateThumb(root_id, (file as { path: string }).path)
      if (selectedRoot) loadFiles(selectedRoot.id, currentPath)
      bumpTreeReload()
    }),
    file_added: typed<'file_added'>(({ sync_root_id }) => {
      if (!selectedRoot || sync_root_id !== selectedRoot.id) return
      loadFiles(selectedRoot.id, currentPath)
      bumpTreeReload()
    }),
    file_deleted: typed<'file_deleted'>(({ sync_root_id, file_id }) => {
      if (!selectedRoot || sync_root_id !== selectedRoot.id) return
      const deleted = allFiles.find((f) => f.id === file_id)
      if (deleted) invalidateThumb(sync_root_id, deleted.path)
      if (deleted?.is_dir && isAncestorOrEqual(deleted.path, currentPath)) {
        go(toFilesUrl(selectedRoot.name, parentPath(deleted.path)))
      } else {
        loadFiles(selectedRoot.id, currentPath)
      }
      bumpTreeReload()
    }),
    file_moved: typed<'file_moved'>(({ sync_root_id, from, to }) => {
      if (!selectedRoot || sync_root_id !== selectedRoot.id) return
      // A folder rename moves the path we are standing in: redirect to the
      // renamed location (to + the part of currentPath below `from`) instead
      // of reloading a path that no longer exists.
      if (from && to && isAncestorOrEqual(from, currentPath)) {
        go(toFilesUrl(selectedRoot.name, to + currentPath.slice(from.length)))
      } else {
        loadFiles(selectedRoot.id, currentPath)
      }
      bumpTreeReload()
    }),
    sync_root_added: (data: unknown) => {
      const m = data as { root?: SyncRoot }
      if (!m.root) return
      setSyncRoots((prev) => (prev.some((r) => r.id === m.root!.id) ? prev : [...prev, m.root!]))
    },
    sync_root_deleted: (data: unknown) => {
      const m = data as { sync_root_id?: string }
      if (!m.sync_root_id) return
      setSyncRoots((prev) => prev.filter((r) => r.id !== m.sync_root_id))
      if (selectedRoot?.id === m.sync_root_id) {
        setSelectedRoot(null)
        go('/files')
      }
    },
    sync_root_renamed: (data: unknown) => {
      const m = data as { sync_root_id?: string; name?: string }
      if (!m.sync_root_id || !m.name) return
      setSyncRoots((prev) => prev.map((r) =>
        r.id === m.sync_root_id ? { ...r, name: m.name! } : r))
      // The /files URL keys on the root name, so a rename of the root we are
      // viewing would orphan the current path — redirect to the new name.
      if (selectedRoot?.id === m.sync_root_id && selectedRoot.name !== m.name) {
        setSelectedRoot({ ...selectedRoot, name: m.name })
        go(toFilesUrl(m.name, currentPath))
      }
    },
    share_added: (data: unknown) => {
      const m = data as { share?: { sync_root_id?: string; target_user_id?: string } }
      if (!m.share) return
      // Giver side: refresh badge map when the new share lives in the
      // active own-root listing.
      if (selectedRoot && !mountMode && m.share.sync_root_id === selectedRoot.id) {
        apiClient.sharesByRoot(selectedRoot.id)
          .then((map) => setSharesByPath(map ?? {}))
          .catch(() => { /* keep stale map */ })
      }
      // Root-list badge map: a new root-anchored share may have appeared.
      if (!selectedRoot && !mountMode) void refreshRootShares(syncRoots)
      // Recipient side: refresh mounts list — a new mount may have
      // appeared for the current user.
      apiClient.sharedMounts.list()
        .then((list) => setMounts(list ?? []))
        .catch(() => { /* keep stale list */ })
    },
    share_revoked: (data: unknown) => {
      const m = data as { share_id?: string }
      if (!m.share_id) return
      // Bob is currently inside the revoked mount → kick him out.
      if (mountMode && mountIDParam === m.share_id) {
        go('/files')
      }
      // Giver side: refresh badge map.
      if (selectedRoot && !mountMode) {
        apiClient.sharesByRoot(selectedRoot.id)
          .then((map) => setSharesByPath(map ?? {}))
          .catch(() => { /* keep stale */ })
      }
      // Root-list badge map: a removed root-anchored share must vanish.
      if (!selectedRoot && !mountMode) void refreshRootShares(syncRoots)
      // Mount-Liste in jedem Fall aktualisieren.
      apiClient.sharedMounts.list()
        .then((list) => setMounts(list ?? []))
        .catch(() => { /* keep stale */ })
    },
  }, () => {
    // Reconnect / tab focus: events may have been missed — refetch.
    loadRoots()
    if (selectedRoot) loadFiles(selectedRoot.id, currentPath)
    bumpTreeReload()
  })
}
