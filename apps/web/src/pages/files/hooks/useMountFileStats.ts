import { useEffect, useState } from 'react'
import type { SharedMount } from '@privasync/api-client'
import { apiClient } from '../../../store/auth'
import type { MountFileStats } from '../rootSections'

// Lazily loads file metadata (id, size, mtime, …) for every file-mount in
// the root list and caches it by mount id. The mount list endpoint carries
// no file metadata, so without this the root list would show stub zeros /
// "Invalid Date". Mirrors useDirStats for sync-root directories — the data
// flows into buildRootSections, feeding both the list row and the detail
// panel. See ADR-0010 / ADR-0012.
export function useMountFileStats(mounts: SharedMount[]): Record<string, MountFileStats> {
  const [cache, setCache] = useState<Record<string, MountFileStats>>({})

  // Stable key over the file-mount ids so the effect only re-runs when the
  // set of file-mounts actually changes, not on every mounts array identity.
  const fileMountIDs = mounts.filter((m) => m.is_file).map((m) => m.id)
  const idsKey = fileMountIDs.join(',')

  useEffect(() => {
    let cancelled = false
    for (const id of fileMountIDs) {
      if (cache[id]) continue
      apiClient.sharedMounts.stats(id)
        .then((stats) => {
          if (cancelled || stats.kind !== 'file') return
          setCache((prev) => prev[id] ? prev : {
            ...prev,
            [id]: {
              id:           stats.id,
              size:         stats.size,
              mtime:        stats.mtime,
              updated_at:   stats.updated_at,
              content_hash: stats.content_hash,
            },
          })
        })
        .catch(() => { /* keep stub; formatDate renders em-dash */ })
    }
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [idsKey])

  // Drop cache entries for mounts that disappeared (left / revoked).
  useEffect(() => {
    setCache((prev) => {
      const next: Record<string, MountFileStats> = {}
      let changed = false
      for (const id of fileMountIDs) {
        if (prev[id]) next[id] = prev[id]
      }
      for (const k of Object.keys(prev)) if (!(k in next)) changed = true
      return changed ? next : prev
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [idsKey])

  return cache
}
