import { useEffect } from 'react'
import type { FileEntry } from '@privasync/api-client'
import type { DetailSelection } from './FileDetailPanel'

// ADR-0012: `mount?` is a Decoration, not a separate Kind. Either
// `dir`-Entry or `file`-Entry can carry a Shared-Mount marker that
// downstream code uses to dispatch between sync-root and mount
// endpoints and to render mount-specific detail rows (giver, access,
// expiry).
import type { SharedMount } from '@privasync/api-client'

export type DirEntry =
  | { kind: 'dir';  name: string; path: string;       mount?: SharedMount }
  | { kind: 'file'; name: string; file: FileEntry;    mount?: SharedMount }

export function isSelectedEntry(sel: DetailSelection | null, entry: DirEntry): boolean {
  if (!sel) return false
  if (sel.kind === 'file') return entry.kind === 'file' && sel.file.id === entry.file.id
  return entry.kind === 'dir' && sel.path === entry.path
}

// Stub selection for a directory; aggregates (count, size, mtime) get filled in
// by the directories/stats fetch in FilesPage.
export function buildDirSelection(dirPath: string, name: string): DetailSelection {
  return { kind: 'dir', name, path: dirPath, itemCount: 0, totalSize: 0, latestMtime: null }
}

export function buildEntries(files: FileEntry[], currentPath: string): DirEntry[] {
  const prefix = currentPath.endsWith('/') ? currentPath : currentPath + '/'
  const seenDirs = new Set<string>()
  const entries: DirEntry[] = []

  for (const file of files) {
    if (!file.path.startsWith(prefix)) continue
    const relative = file.path.slice(prefix.length)
    if (!relative) continue
    const slashIdx = relative.indexOf('/')
    if (slashIdx === -1) {
      if (file.is_dir) {
        if (!seenDirs.has(relative)) {
          seenDirs.add(relative)
          entries.push({ kind: 'dir', name: relative, path: prefix + relative })
        }
      } else {
        entries.push({ kind: 'file', name: relative, file })
      }
    } else {
      const dirName = relative.slice(0, slashIdx)
      if (!seenDirs.has(dirName)) {
        seenDirs.add(dirName)
        entries.push({ kind: 'dir', name: dirName, path: prefix + dirName })
      }
    }
  }

  entries.sort((a, b) => {
    if (a.kind !== b.kind) return a.kind === 'dir' ? -1 : 1
    return a.name.localeCompare(b.name)
  })
  return entries
}

// Thumbnail source for a file row: where RowThumb fetches the preview from.
// `rootID` also scopes the in-memory thumb cache key. For Shared-Mount files
// `mountID` routes the fetch through the mount endpoint. See ADR-0010.
export type ThumbHost = { rootID: string; path: string; mountID?: string }

// thumbHostFor resolves the thumbnail source for an entry, or null when no
// thumbnail applies (directories, or own files without an open sync root).
//
// A file entry carrying a `mount` is always a file-mount (only
// buildRootSections decorates entries with mount; buildEntries does not), so
// the mount-relative sub-path is "/" — the mount root *is* the file. Passing
// the giver-absolute file.path would double the prefix server-side.
//
// `mountMode` covers the other mount case: a plain file browsed INSIDE a
// dir-mount. Then selectedRoot is the share facade (id = share id) and the
// fetch must route through the mount endpoint with the file's mount-relative
// path — otherwise the share id is treated as a sync-root id and no thumbnail
// loads. See ADR-0010 / ADR-0012.
export function thumbHostFor(
  entry: DirEntry,
  selectedRoot: { id: string } | null | undefined,
  mountMode = false,
): ThumbHost | null {
  if (entry.kind !== 'file') return null
  if (entry.mount) {
    return { rootID: 'mount:' + entry.mount.id, path: '/', mountID: entry.mount.id }
  }
  if (selectedRoot) {
    return mountMode
      ? { rootID: 'mount:' + selectedRoot.id, path: entry.file.path, mountID: selectedRoot.id }
      : { rootID: selectedRoot.id, path: entry.file.path }
  }
  return null
}

export function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

export function formatDate(iso: string, locale: string): string {
  const d = new Date(iso)
  // Guard against empty/placeholder/unparseable input so the UI never
  // shows the literal "Invalid Date" — fall back to a neutral em-dash.
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString(locale, {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit',
  })
}

export function pathSegments(path: string): { label: string; path: string }[] {
  if (path === '/') return []
  const parts = path.split('/').filter(Boolean)
  return parts.map((part, i) => ({
    label: part,
    path: '/' + parts.slice(0, i + 1).join('/'),
  }))
}

// isAncestorOrEqual reports whether `ancestor` equals or is a parent of
// `descendant` in slash-separated path notation.
export function isAncestorOrEqual(ancestor: string, descendant: string): boolean {
  if (ancestor === descendant) return true
  const a = ancestor.endsWith('/') ? ancestor : ancestor + '/'
  return descendant.startsWith(a)
}

export function parentPath(path: string): string {
  const idx = path.lastIndexOf('/')
  if (idx <= 0) return '/'
  return path.slice(0, idx)
}

// Build a /files/... URL from sync-root name + internal path.
export function toFilesUrl(rootName: string, path = '/'): string {
  const parts = [encodeURIComponent(rootName)]
  if (path !== '/') {
    parts.push(...path.split('/').filter(Boolean).map(encodeURIComponent))
  }
  return '/files/' + parts.join('/')
}

// Build a /files/_m/{shareID}/... URL for browsing inside a Shared Mount.
// The `_m` prefix is reserved so it cannot collide with a user-chosen
// sync-root name. See ADR-0010.
export function toMountUrl(shareID: string, path = '/'): string {
  const parts = path.split('/').filter(Boolean).map(encodeURIComponent)
  return '/files/_m/' + shareID + (parts.length ? '/' + parts.join('/') : '')
}

export function useClickOutside(
  ref:     React.RefObject<HTMLElement | null>,
  open:    boolean,
  onClose: () => void,
) {
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose()
    }
    if (open) document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])
}
