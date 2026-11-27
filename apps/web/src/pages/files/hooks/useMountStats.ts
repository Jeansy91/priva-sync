import { useEffect } from 'react'
import { apiClient } from '../../../store/auth'
import type { DetailSelection } from '../FileDetailPanel'

// Lazy-loads mount aggregates (dir mount) or file meta (file mount) for
// the active mount selection and folds the result into detailSelection.
// Initial selection renders stub zeros until this hook resolves. ADR-0012.
export function useMountStats(
  detailSelection: DetailSelection | null,
  setDetailSelection: (updater: (prev: DetailSelection | null) => DetailSelection | null) => void,
) {
  const mountID = detailSelection?.mount?.id
  useEffect(() => {
    if (!mountID) return
    let cancelled = false
    apiClient.sharedMounts.stats(mountID)
      .then((stats) => {
        if (cancelled) return
        setDetailSelection((prev) => {
          if (!prev || prev.mount?.id !== mountID) return prev
          if (stats.kind === 'file' && prev.kind === 'file') {
            return {
              ...prev,
              file: {
                ...prev.file,
                id:           stats.id,
                size:         stats.size,
                mtime:        stats.mtime,
                content_hash: stats.content_hash,
                updated_at:   stats.updated_at,
              },
            }
          }
          if (stats.kind === 'dir' && prev.kind === 'dir') {
            return {
              ...prev,
              itemCount:   stats.item_count,
              totalSize:   stats.total_size,
              latestMtime: stats.latest_mtime ?? null,
            }
          }
          return prev
        })
      })
      .catch(() => { /* keep stubs */ })
    return () => { cancelled = true }
  }, [mountID, setDetailSelection])
}
