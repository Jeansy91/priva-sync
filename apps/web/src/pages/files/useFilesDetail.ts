import { useMemo } from 'react'
import type { SyncRoot, FileEntry } from '@privasync/api-client'
import { useDirStats } from './useDirStats'
import { useMountStats } from './hooks/useMountStats'
import { buildDirSelection } from './helpers'
import { basename } from './filesPagePrefs'
import type { DetailSelection } from './FileDetailPanel'

interface UseFilesDetailInput {
  selectedRoot:       SyncRoot | null
  currentPath:        string
  allFiles:           FileEntry[]
  syncRoots:          SyncRoot[]
  // Sync-root list row highlighted via single-click — only meaningful at
  // /files where no sync root is open yet.
  selectedSyncRootID: string | null
  detailSelection:    DetailSelection | null
  setDetailSelection: (updater: (prev: DetailSelection | null) => DetailSelection | null) => void
}

/**
 * Derives what the detail panel shows: the root it's focused on, whether
 * that's the sync-root list level (no root open, a list row highlighted
 * instead), and the effective selection with lazily-loaded directory/mount
 * stats folded in. Falls back to "the current directory" when nothing is
 * explicitly selected, so the panel always has something to render.
 *
 * Bundles the two stats hooks (useDirStats, useMountStats) since both only
 * exist to enrich this derived selection — nothing else in the page reads
 * their raw output.
 */
export function useFilesDetail({
  selectedRoot, currentPath, allFiles, syncRoots, selectedSyncRootID,
  detailSelection, setDetailSelection,
}: UseFilesDetailInput) {
  // Resolves to either the navigated root or — when the user is on the
  // sync-root list (/files) — the row they highlighted via single click.
  const detailRoot = selectedRoot
    ?? syncRoots.find((r) => r.id === selectedSyncRootID)
    ?? null
  const isListLevel = !selectedRoot && detailRoot !== null

  const effectiveDirPath: string | null =
    detailSelection?.kind === 'dir'
      ? detailSelection.path
      : (!detailSelection && selectedRoot ? currentPath
        : isListLevel ? '/' : null)

  const dirStatsCache = useDirStats({ detailRoot, effectiveDirPath, currentPath, allFiles })

  useMountStats(detailSelection, setDetailSelection)

  // Falls back to the current directory when no entry is selected.
  const effectiveSelection = useMemo<DetailSelection | null>(() => {
    const base: DetailSelection | null = detailSelection
      ? detailSelection
      : (selectedRoot ? buildDirSelection(
          currentPath,
          currentPath === '/' || currentPath === '' ? selectedRoot.name : basename(currentPath),
        ) : isListLevel && detailRoot ? buildDirSelection('/', detailRoot.name)
        : null)
    if (!base || base.kind !== 'dir') return base
    const stats = dirStatsCache[base.path]
    if (!stats) return base
    return {
      ...base,
      itemCount:   stats.item_count,
      totalSize:   stats.total_size,
      latestMtime: stats.latest_mtime,
    }
  }, [detailSelection, selectedRoot, currentPath, dirStatsCache, isListLevel, detailRoot])

  return { detailRoot, isListLevel, effectiveSelection }
}
