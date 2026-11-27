import { useMemo } from 'react'
import type { SyncRoot, SharedMount, FileEntry, PathShareInfo } from '@privasync/api-client'
import { useTableSort, type SortDir } from '../../hooks/useTableSort'
import { buildEntries, type DirEntry } from './helpers'
import { entryKey, type FilesSortKey } from './filesViewTypes'
import { buildRootSections, rootSharesByPath, type MountFileStats } from './rootSections'
import type { TableSection } from './FilesTableView'

type EntryComparators = Record<FilesSortKey, (a: DirEntry, b: DirEntry) => number>

// Orders a listing the in-root way: folders first (only reordered on a name
// sort, since dirs carry no size/mtime), then files by the active key.
// Shared by the in-root listing and the sync-root list so both honour the
// same persisted `files` sort state.
function orderEntries(
  entries:     DirEntry[],
  sortKey:     FilesSortKey | null,
  sortDir:     SortDir | null,
  comparators: EntryComparators,
): DirEntry[] {
  const dirs  = entries.filter((e) => e.kind === 'dir')
  const files = entries.filter((e) => e.kind === 'file')
  if (sortKey === 'name') {
    dirs.sort(comparators.name)
    if (sortDir === 'desc') dirs.reverse()
  }
  if (sortKey) {
    files.sort(comparators[sortKey])
    if (sortDir === 'desc') files.reverse()
  }
  return [...dirs, ...files]
}

interface UseFilesEntriesInput {
  selectedRoot: SyncRoot | null
  currentPath:  string
  allFiles:     FileEntry[]
  syncRoots:    SyncRoot[]
  mounts:       SharedMount[]
  /** Lazily-loaded file metadata for file-mounts, keyed by mount id. */
  mountFileStats: Record<string, MountFileStats>
  /** Owner-side share map per root id, used for badge decoration. */
  rootSharesByRootID: Record<string, PathShareInfo[]>
  t: (key: string) => string
}

/**
 * Derives every list the rendering code needs from the raw inventory:
 * in-root entries (file/dir), sorted output, and the root-list section
 * model. Pure derivation — no effects, no setters.
 */
export function useFilesEntries(input: UseFilesEntriesInput) {
  const { selectedRoot, currentPath, allFiles, syncRoots, mounts, mountFileStats, rootSharesByRootID, t } = input

  const entries = useMemo(
    () => selectedRoot ? buildEntries(allFiles, currentPath) : [],
    [allFiles, currentPath, selectedRoot],
  )

  const fileEntries = useMemo(() => entries.filter((e) => e.kind === 'file'), [entries])

  const fileSortComparators = useMemo<EntryComparators>(() => ({
    name:  (a: DirEntry, b: DirEntry) => a.name.localeCompare(b.name),
    size:  (a: DirEntry, b: DirEntry) =>
      (a.kind === 'file' ? a.file.size : 0) - (b.kind === 'file' ? b.file.size : 0),
    mtime: (a: DirEntry, b: DirEntry) => {
      const at = a.kind === 'file' ? a.file.mtime : ''
      const bt = b.kind === 'file' ? b.file.mtime : ''
      return at.localeCompare(bt)
    },
  }), [])

  const filesSort = useTableSort(fileEntries, {
    storageKey: 'files',
    comparators: fileSortComparators,
  })

  // Sync-root listing reuses the same view components as a normal directory.
  // Each root becomes a synthetic dir entry whose path encodes its name so
  // entryKey stays unique and we can look the root back up on activate.
  const rootByName = useMemo(
    () => new Map(syncRoots.map((r) => [r.name, r])),
    [syncRoots],
  )

  const rootSectionsRaw = useMemo(
    () => buildRootSections(syncRoots, mounts, mountFileStats),
    [syncRoots, mounts, mountFileStats],
  )
  const rootListSharesByPath = useMemo(
    () => rootSharesByPath(syncRoots, rootSharesByRootID),
    [syncRoots, rootSharesByRootID],
  )
  // Root list shares the in-root sort state and comparators (same `files`
  // storage key), so the chosen order persists across both levels. File-mounts
  // carry real size/mtime; own roots and dir-mounts stay stable on those keys.
  const rootTableSections = useMemo<TableSection[]>(() => {
    return rootSectionsRaw.map((s) => ({
      id:      s.id,
      header:  s.id === 'own' ? t('files.sectionOwn') : t('files.sectionShared'),
      entries: orderEntries(s.entries, filesSort.sortKey, filesSort.sortDir, fileSortComparators),
    }))
  }, [rootSectionsRaw, filesSort.sortKey, filesSort.sortDir, fileSortComparators, t])

  const displayEntries = useMemo(
    () => orderEntries(entries, filesSort.sortKey, filesSort.sortDir, fileSortComparators),
    [entries, filesSort.sortKey, filesSort.sortDir, fileSortComparators],
  )

  const orderedKeys = useMemo(
    () => displayEntries.map((e) => entryKey(e)),
    [displayEntries],
  )

  return {
    entries, fileEntries,
    filesSort,
    rootByName, rootSectionsRaw, rootListSharesByPath, rootTableSections,
    displayEntries, orderedKeys,
  }
}
