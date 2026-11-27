import { useState, useEffect } from 'react'
import type { SyncRoot, FileEntry } from '@privasync/api-client'
import { apiClient } from '../../store/auth'

export interface DirStats {
  item_count:   number
  total_size:   number
  latest_mtime: string | null
}

interface UseDirStatsInput {
  /** The root the detail panel is currently focused on. */
  detailRoot:       SyncRoot | null
  /** The directory path stats should be loaded for, or null. */
  effectiveDirPath: string | null
  /** Drives cache invalidation on listing refresh. */
  currentPath:      string
  /** Same trigger — file list change implies stats may be stale. */
  allFiles:         FileEntry[]
}

/**
 * Lazily loads + caches directory stats (item count, total size, latest
 * mtime) so the detail panel can show real numbers instead of the stub
 * zeros. The cache is keyed by path only — switching roots invalidates
 * the whole map, refreshing the current path drops just its entry.
 */
export function useDirStats(input: UseDirStatsInput): Record<string, DirStats> {
  const { detailRoot, effectiveDirPath, currentPath, allFiles } = input

  const [dirStatsCache, setDirStatsCache] = useState<Record<string, DirStats>>({})

  useEffect(() => {
    if (!detailRoot || !effectiveDirPath) return
    if (dirStatsCache[effectiveDirPath]) return
    let cancelled = false
    apiClient.files.directoryStats(detailRoot.id, effectiveDirPath)
      .then((stats) => {
        if (cancelled) return
        setDirStatsCache((prev) => ({
          ...prev,
          [effectiveDirPath]: {
            item_count:   stats.item_count,
            total_size:   stats.total_size,
            latest_mtime: stats.latest_mtime ?? null,
          },
        }))
      })
      .catch(() => { /* Panel zeigt dann nur die Stub-Nullen */ })
    return () => { cancelled = true }
  }, [detailRoot, effectiveDirPath, dirStatsCache])

  // Drop the current path's entry when the listing under it changes —
  // otherwise the detail panel would keep stale totals after an upload/delete.
  useEffect(() => {
    setDirStatsCache((prev) => {
      if (!prev[currentPath]) return prev
      const next = { ...prev }
      delete next[currentPath]
      return next
    })
  }, [currentPath, allFiles])

  // Switching roots invalidates every entry — paths repeat across roots.
  useEffect(() => { setDirStatsCache({}) }, [detailRoot?.id])

  return dirStatsCache
}
