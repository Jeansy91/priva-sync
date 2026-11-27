import type { SharedMount } from '@privasync/api-client'
import type { DetailSelection } from './FileDetailPanel'
import type { FilesViewMode } from './filesViewTypes'

export const TREE_WIDTH_KEY     = 'privasync.filesTreeWidth'
export const TREE_WIDTH_MIN     = 160
export const TREE_WIDTH_MAX     = 480
export const TREE_WIDTH_DEFAULT = 240

export const DETAILS_VISIBLE_KEY = 'privasync.filesDetailsVisible'
export const VIEW_MODE_KEY       = 'privasync.filesViewMode'

export function basename(p: string): string {
  const i = p.lastIndexOf('/')
  return i < 0 ? p : p.slice(i + 1)
}

// Synthesises an initial DetailSelection for a Shared Mount row. Stats
// arrive lazily via /shared-mounts/{id}/stats — for the first paint we
// stub size/mtime/etc. so the detail panel renders straight away with
// just the mount facets (giver, access, expiry). ADR-0012.
export function buildMountSelection(m: SharedMount): DetailSelection {
  if (m.is_file) {
    return {
      kind: 'file',
      mount: m,
      file: {
        id:           'mount:' + m.id,
        path:         m.path,
        size:         0,
        mtime:        '',
        content_hash: '',
        is_deleted:   false,
        updated_at:   '',
      },
    }
  }
  return {
    kind:        'dir',
    name:        m.name,
    path:        '/' + m.name,
    itemCount:   0,
    totalSize:   0,
    latestMtime: null,
    mount:       m,
  }
}

// Resolves the next detail selection when a Shared Mount row is clicked.
// Re-clicking the already-selected mount keeps the current — by then
// stats-loaded — selection instead of resetting to a fresh stub, which
// would otherwise blank itemCount/size back to zero (useMountStats only
// refetches when the mount id changes). The mount decoration is refreshed
// so facets (access, expiry) stay in sync if the mount itself changed.
export function nextMountSelection(
  current: DetailSelection | null,
  mount: SharedMount,
): DetailSelection {
  if (current && current.mount?.id === mount.id) {
    return { ...current, mount }
  }
  return buildMountSelection(mount)
}

export function readStoredViewMode(): FilesViewMode {
  if (typeof window === 'undefined') return 'table'
  const v = window.localStorage.getItem(VIEW_MODE_KEY)
  return v === 'grid' ? 'grid' : 'table'
}
