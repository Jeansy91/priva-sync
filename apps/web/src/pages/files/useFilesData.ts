import { useState, useEffect, useCallback } from 'react'
import type { SyncRoot, SharedMount, FileEntry, PathShareInfo } from '@privasync/api-client'
import { apiClient } from '../../store/auth'

interface UseFilesDataInput {
  selectedRoot: SyncRoot | null
  currentPath:  string
  /** True while the URL points to a mount (giver-side share map is skipped). */
  mountMode:    boolean
  t: (key: string) => string
}

/**
 * Owns the file-listing data layer of FilesPage:
 *   - sync-root / mount inventory (loaded once + on SSE reconnect)
 *   - giver-side share map for the active own-root
 *   - paged file list for the current path, including cursor pagination
 *
 * Setters are exposed because SSE handlers in the page still need to
 * patch the lists optimistically (sync_root_added/deleted, share_added,
 * etc.). loadRoots/loadFiles are exposed for the SSE reconnect refetch.
 */
export function useFilesData(input: UseFilesDataInput) {
  const { selectedRoot, currentPath, mountMode, t } = input

  const [syncRoots, setSyncRoots]       = useState<SyncRoot[]>([])
  const [mounts, setMounts]             = useState<SharedMount[]>([])
  const [sharesByPath, setSharesByPath] = useState<Record<string, PathShareInfo[]>>({})
  const [allFiles, setAllFiles]         = useState<FileEntry[]>([])
  const [loading, setLoading]           = useState(false)
  const [error, setError]               = useState<string | null>(null)
  const [filesNextCursor, setFilesNextCursor] = useState('')

  const loadRoots = useCallback(async () => {
    setLoading(true)
    try {
      const [roots, mountList] = await Promise.all([
        apiClient.files.listSyncRoots(),
        apiClient.sharedMounts.list().catch(() => [] as SharedMount[]),
      ])
      setSyncRoots(roots ?? [])
      setMounts(mountList ?? [])
    } catch {
      setError(t('files.loadRootsFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { loadRoots() }, [loadRoots])

  const loadFiles = useCallback(async (rootID: string, path: string) => {
    setLoading(true)
    setError(null)
    try {
      // Shared Mount: rootID is actually the share-id. Mount endpoint
      // returns absolute paths in the giver's root; we strip the
      // share.path prefix so the existing buildEntries / breadcrumb code
      // can treat them as relative to the mount root. See ADR-0010.
      const m = mounts.find((mm) => mm.id === rootID)
      if (m) {
        const res = await apiClient.sharedMounts.files(rootID, path)
        // Strip the share-path prefix to get mount-relative paths. A sync-root
        // share has m.path "/", whose trailing slash is also the leading
        // slash — slicing it off would eat the "/" of every child ("/foo" ->
        // "foo") and buildEntries drops them, rendering the mount empty. Drop
        // the root case to an empty prefix so the leading slash survives.
        const base = m.path === '/' ? '' : m.path
        const stripped: FileEntry[] = (res.entries ?? []).map((e) => ({
          id:           'mount:' + rootID + ':' + e.path,
          path:         e.path.startsWith(base) ? (e.path.slice(base.length) || '/') : e.path,
          size:         e.size,
          mtime:        e.mtime ?? '',
          content_hash: e.content_hash ?? '',
          is_deleted:   false,
          is_dir:       e.is_dir,
          updated_at:   e.mtime ?? '',
        }))
        setAllFiles(stripped)
        setFilesNextCursor('')
        return
      }
      const res = await apiClient.files.list(rootID, path)
      setAllFiles(res.files ?? [])
      setFilesNextCursor(res.next_cursor ?? '')
    } catch {
      setError(t('files.loadFilesFailed'))
    } finally {
      setLoading(false)
    }
  }, [mounts, t])

  const loadMoreFiles = useCallback(async () => {
    if (!selectedRoot || !filesNextCursor || loading) return
    setLoading(true)
    try {
      const res = await apiClient.files.list(selectedRoot.id, currentPath, {
        cursor: filesNextCursor,
      })
      setAllFiles((prev) => prev.concat(res.files ?? []))
      setFilesNextCursor(res.next_cursor ?? '')
    } catch {
      setError(t('files.loadFilesFailed'))
    } finally {
      setLoading(false)
    }
  }, [selectedRoot, filesNextCursor, loading, currentPath, t])

  useEffect(() => {
    if (selectedRoot) {
      loadFiles(selectedRoot.id, currentPath)
    } else {
      setAllFiles([])
      setFilesNextCursor('')
    }
    // loadFiles intentionally omitted from deps — it changes when `mounts`
    // updates, which would refetch the listing every time a share lands.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedRoot, currentPath])

  // Giver-side share map for the active own-root. Skipped in mount-mode
  // because Bob's selectedRoot.id is a share-id, not a sync-root id.
  useEffect(() => {
    if (!selectedRoot || mountMode) {
      setSharesByPath({})
      return
    }
    apiClient.sharesByRoot(selectedRoot.id)
      .then((m) => setSharesByPath(m ?? {}))
      .catch(() => setSharesByPath({}))
  }, [selectedRoot, mountMode])

  return {
    syncRoots, setSyncRoots,
    mounts, setMounts,
    sharesByPath, setSharesByPath,
    allFiles, setAllFiles,
    loading, setLoading,
    error, setError,
    filesNextCursor,
    loadRoots, loadFiles, loadMoreFiles,
  }
}
