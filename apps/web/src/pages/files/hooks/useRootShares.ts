import { useCallback, useEffect, useState } from 'react'
import type { PathShareInfo, SyncRoot } from '@privasync/api-client'
import { apiClient } from '../../../store/auth'

// Root-level share map for the root list. Aggregates shares anchored at
// path '/' per own root so the section view can render a badge on the
// root row itself. Sub-path shares are intentionally ignored.
// Exposes refreshRootShares so SSE callbacks can re-trigger after
// share_added / share_revoked. See ADR-0010.
export function useRootShares(
  syncRoots: SyncRoot[],
  selectedRoot: SyncRoot | null,
  mountMode: boolean,
  urlRootName: string | null | undefined,
) {
  const [rootSharesByRootID, setRootSharesByRootID] = useState<Record<string, PathShareInfo[]>>({})

  const refreshRootShares = useCallback(async (roots: SyncRoot[]) => {
    const pairs = await Promise.all(roots.map(async (r) => {
      try {
        const m = await apiClient.sharesByRoot(r.id)
        const own = (m && m['/']) ?? []
        return [r.id, own] as const
      } catch {
        return [r.id, [] as PathShareInfo[]] as const
      }
    }))
    setRootSharesByRootID(Object.fromEntries(pairs.filter(([, v]) => v.length > 0)))
  }, [])

  useEffect(() => {
    // Skip while the URL still names a root: selectedRoot has not settled
    // yet but is about to. Issuing the parallel sharesByRoot here would
    // waste a round-trip and confuse mock-driven tests counting calls.
    if (selectedRoot || mountMode || syncRoots.length === 0) return
    if (urlRootName) return
    void refreshRootShares(syncRoots)
  }, [syncRoots, selectedRoot, mountMode, urlRootName, refreshRootShares])

  return { rootSharesByRootID, refreshRootShares }
}
