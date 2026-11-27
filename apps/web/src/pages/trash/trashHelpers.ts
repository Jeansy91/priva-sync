import type { SyncRoot, TrashEntry } from '@privasync/api-client'

export interface TrashEntryWithRoot extends TrashEntry {
  rootID:   string
  rootName: string
}

export type PurgeTarget =
  | { kind: 'one';      entry: TrashEntryWithRoot }
  | { kind: 'all';      count: number }
  | { kind: 'selected'; count: number }
  | { kind: 'list';     entries: TrashEntryWithRoot[] }

export function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

export function formatDate(iso: string | null, locale: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString(locale, {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit',
  })
}

export function fileName(path: string): string {
  return path.split('/').filter(Boolean).pop() ?? path
}

export function dirPath(path: string): string {
  const parts = path.split('/').filter(Boolean)
  parts.pop()
  return parts.length ? '/' + parts.join('/') + '/' : '/'
}

export type TreeNode =
  | { kind: 'root';   id: string; name: string; children: TreeNode[]; fileIds: string[] }
  | { kind: 'folder'; id: string; name: string; children: TreeNode[]; fileIds: string[] }
  | { kind: 'file';   id: string; entry: TrashEntryWithRoot }

// Sort children recursively: folders before files, alphabetical
// (case-insensitive). Invoked per node at the end of buildTrashTree.
function sortChildren(node: TreeNode): void {
  if (node.kind === 'file') return
  for (const c of node.children) sortChildren(c)
  node.children.sort((a, b) => {
    const aFile = a.kind === 'file'
    const bFile = b.kind === 'file'
    if (aFile !== bFile) return aFile ? 1 : -1
    const an = a.kind === 'file' ? fileName(a.entry.path) : a.name
    const bn = b.kind === 'file' ? fileName(b.entry.path) : b.name
    return an.localeCompare(bn, undefined, { sensitivity: 'base' })
  })
}

/**
 * Build the folder-view tree from the flat trash listing. Each sync root
 * becomes a top-level node; intermediate paths are folded into folder
 * nodes, and trashed files land as leaves. Empty roots (no trash) are
 * omitted so the user does not see expandable but empty branches.
 */
export function buildTrashTree(
  roots: SyncRoot[],
  entries: TrashEntryWithRoot[],
): TreeNode[] {
  // Roots without trash entries are hidden — otherwise the folder view shows
  // empty branches that the user cannot expand.
  const rootsWithEntries = roots.filter((root) =>
    entries.some((e) => e.rootID === root.id),
  )
  return rootsWithEntries.map((root) => {
    const rootEntries = entries.filter((e) => e.rootID === root.id)
    const rootNode: TreeNode = {
      kind: 'root',
      id: `root:${root.id}`,
      name: root.name,
      children: [],
      fileIds: [],
    }
    const folderIndex = new Map<string, TreeNode & { kind: 'folder' }>()
    for (const e of rootEntries) {
      const parts = e.path.split('/').filter(Boolean)
      const fname = parts.pop() ?? e.path
      let parent: TreeNode & { kind: 'root' | 'folder' } = rootNode
      let prefix = ''
      for (const seg of parts) {
        prefix = prefix ? `${prefix}/${seg}` : seg
        const key = `${root.id}:${prefix}`
        let folder = folderIndex.get(key)
        if (!folder) {
          folder = { kind: 'folder', id: `folder:${key}`, name: seg, children: [], fileIds: [] }
          folderIndex.set(key, folder)
          parent.children.push(folder)
        }
        parent = folder
      }
      parent.fileIds.push(e.id)
      parent.children.push({ kind: 'file', id: e.id, entry: { ...e, path: '/' + (parts.length ? parts.join('/') + '/' : '') + fname } })
    }
    sortChildren(rootNode)
    return rootNode
  })
}

export function collectFileIds(node: TreeNode): string[] {
  if (node.kind === 'file') return [node.id]
  return node.children.flatMap(collectFileIds)
}

export function aggregateSize(node: TreeNode): number {
  if (node.kind === 'file') return node.entry.size
  return node.children.reduce((sum, c) => sum + aggregateSize(c), 0)
}

export function latestDeletedAt(node: TreeNode): string | null {
  if (node.kind === 'file') return node.entry.deleted_at
  let latest: string | null = null
  for (const c of node.children) {
    const d = latestDeletedAt(c)
    if (d && (!latest || new Date(d).getTime() > new Date(latest).getTime())) latest = d
  }
  return latest
}

export function collectFileEntries(node: TreeNode): TrashEntryWithRoot[] {
  if (node.kind === 'file') return [node.entry]
  return node.children.flatMap(collectFileEntries)
}

export interface TrashLabels {
  colFile:      string
  colSize:      string
  colDeletedAt: string
  selectAll:    string
  expand:       string
  collapse:     string
  restore:      string
  purgeOne:     string
}
