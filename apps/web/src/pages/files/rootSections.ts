import type { SyncRoot, SharedMount, PathShareInfo } from '@privasync/api-client'
import type { DirEntry } from './helpers'

// ADR-0012: Eintraege in der Root-Liste sind ganz normale DirEntries
// mit optionalem mount?. Eine eigene Mount-Variante gibt es nicht
// mehr — die Decoration treibt alles weitere.
//
// `id` steht stabil fuer den Sektion-Slot (Header-Uebersetzung,
// Collapse-State, Section-Key); der Caller mappt sie auf den
// uebersetzten Header-Text.

export type RootSectionID = 'own' | 'shared'

export type RootListSection = {
  id:      RootSectionID
  entries: DirEntry[]
}

// Lazily-loaded metadata for a file-mount, keyed by mount id. The mount
// list endpoint carries no file metadata (SharedMount has none), so these
// are fetched on demand via sharedMounts.stats and folded into the entry —
// mirroring how sync-root directory stats flow into the detail panel.
export type MountFileStats = {
  id:           string
  size:         number
  mtime:        string
  updated_at:   string
  content_hash: string
}

export function buildRootSections(
  roots:      SyncRoot[],
  mounts:     SharedMount[],
  fileStats:  Record<string, MountFileStats> = {},
): RootListSection[] {
  const sections: RootListSection[] = []
  if (roots.length > 0) {
    const sortedRoots = roots.slice().sort((a, b) => a.name.localeCompare(b.name))
    sections.push({
      id: 'own',
      entries: sortedRoots.map((r): DirEntry => ({
        kind: 'dir',
        name: r.name,
        path: '/' + r.name,
      })),
    })
  }
  if (mounts.length > 0) {
    // Folders before files, alphabetical within each group — mirrors
    // buildEntries so the root list and in-root listings sort the same way.
    const sorted = mounts.slice().sort((a, b) => {
      if (a.is_file !== b.is_file) return a.is_file ? 1 : -1
      return a.name.localeCompare(b.name)
    })
    sections.push({
      id: 'shared',
      entries: sorted.map((m): DirEntry => m.is_file
        ? {
            kind:  'file',
            name:  m.name,
            mount: m,
            file:  (() => {
              const stats = fileStats[m.id]
              return {
                id:           stats?.id ?? 'mount:' + m.id,
                path:         m.path,
                size:         stats?.size ?? 0,
                mtime:        stats?.mtime ?? '',
                content_hash: stats?.content_hash ?? '',
                is_deleted:   false,
                updated_at:   stats?.updated_at ?? '',
              }
            })(),
          }
        : {
            kind:  'dir',
            name:  m.name,
            path:  '/' + m.name,
            mount: m,
          },
      ),
    })
  }
  return sections
}

// Build the per-path share map that FilesTableView already consumes,
// keyed by the displayed root path (e.g. "/Photos"). Lets the root list
// render ShareBadges through the same code path as in-root listings.
export function rootSharesByPath(
  roots:        SyncRoot[],
  sharesByRoot: Record<string, PathShareInfo[]>,
): Record<string, PathShareInfo[]> {
  const out: Record<string, PathShareInfo[]> = {}
  for (const r of roots) {
    const list = sharesByRoot[r.id]
    if (list && list.length > 0) out['/' + r.name] = list
  }
  return out
}
