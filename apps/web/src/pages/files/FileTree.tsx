import { useCallback, useEffect, useRef, useState } from 'react'
import { ChevronRight, Folder, Loader2 } from 'lucide-react'
import { cn } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import type { SyncRoot, FileEntry, SharedMount } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { directSubdirs } from './dirNodes'
import type { FilesDnd } from './filesViewTypes'

interface FileTreeProps {
  roots: SyncRoot[]
  // Shared mounts to list alongside own sync-roots. Only dir-mounts are
  // browsable in the tree; file-mounts (single files) are filtered out.
  mounts?: SharedMount[]
  activeRootID: string | null
  activePath: string
  onNavigate: (rootID: string, path: string) => void
  // Increment to invalidate cache and refetch every currently-loaded path.
  // Owner bumps this on file events / SSE reconnects.
  reloadToken?: number
  // Drag-and-drop move drop targets. Only nodes of the active root accept a
  // drop — the move endpoint works within a single root.
  dnd?: Pick<FilesDnd, 'dropProps' | 'isDropActive'>
}

// Cache key for loaded directory contents: rootID + parentPath.
function cacheKey(rootID: string, path: string): string {
  return `${rootID}::${path}`
}

// Fetch one directory level from a Shared Mount and adapt it to the flat
// FileEntry shape directSubdirs already consumes. The mount endpoint returns
// the direct children of `path`, but `e.path` is GIVER-ABSOLUTE (e.g.
// "/Team/Reports") — so we rebuild a mount-relative child path from the
// requested `path` + `e.name`. Using `e.path` would leak the giver's root
// folder name into the tree as a phantom self-child.
async function fetchMountLevel(mountID: string, path: string): Promise<FileEntry[]> {
  const res = await apiClient.sharedMounts.files(mountID, path)
  return (res.entries ?? []).map((e) => {
    const childPath = path === '/' ? '/' + e.name : path + '/' + e.name
    return {
      id:           'mount:' + mountID + ':' + childPath,
      path:         childPath,
      size:         e.size,
      mtime:        e.mtime ?? '',
      content_hash: e.content_hash ?? '',
      is_deleted:   false,
      updated_at:   '',
      is_dir:       e.is_dir,
    }
  })
}

// Returns all ancestor paths of a given path including '/' but excluding
// the path itself. Example: '/a/b/c' -> ['/', '/a', '/a/b']. Used on reload
// to expand the tree down to the active directory.
function ancestorPaths(path: string): string[] {
  const parts = path.split('/').filter(Boolean)
  const out: string[] = ['/']
  for (let i = 0; i < parts.length - 1; i++) {
    out.push('/' + parts.slice(0, i + 1).join('/'))
  }
  return out
}

// Lazy-loaded tree sidebar. Each expand fetches one directory level — via
// files.list for own sync-roots, via sharedMounts.files for dir-mounts — and
// caches the result so re-renders and re-expands don't trigger an API call.
// Expand state lives in component state and survives as long as the tree is
// mounted (the sidebar isn't unmounted until the user disables it in the
// settings).
export function FileTree({ roots, mounts = [], activeRootID, activePath, onNavigate, reloadToken = 0, dnd }: FileTreeProps) {
  const { t } = useTranslation()
  // Only dir-mounts are browsable; file-mounts have no directory children.
  const dirMounts = mounts.filter((m) => !m.is_file)
  // Top-level ids that are mounts. The recursion keeps rootID fixed per
  // top-level node, so this set tells fetchInto which endpoint to hit at
  // any depth. Mount and sync-root ids live in separate tables, so the
  // membership test never aliases an own root.
  const mountIDs = new Set(dirMounts.map((m) => m.id))
  // Ref so fetchInto can dispatch without listing mounts as a dep (which
  // would rebuild the callback every render and churn the effects below).
  const mountIDsRef = useRef(mountIDs)
  mountIDsRef.current = mountIDs
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [files, setFiles] = useState<Map<string, FileEntry[]>>(new Map())
  const [loading, setLoading] = useState<Set<string>>(new Set())

  const fetchInto = useCallback(async (rootID: string, path: string) => {
    const key = cacheKey(rootID, path)
    setLoading((prev) => new Set(prev).add(key))
    try {
      const entries = mountIDsRef.current.has(rootID)
        ? await fetchMountLevel(rootID, path)
        : (await apiClient.files.list(rootID, path)).files ?? []
      setFiles((prev) => new Map(prev).set(key, entries))
    } catch {
      setFiles((prev) => new Map(prev).set(key, []))
    } finally {
      setLoading((prev) => {
        const next = new Set(prev)
        next.delete(key)
        return next
      })
    }
  }, [])

  const ensureLoaded = useCallback(async (rootID: string, path: string) => {
    const key = cacheKey(rootID, path)
    if (files.has(key) || loading.has(key)) return
    await fetchInto(rootID, path)
  }, [files, loading, fetchInto])

  // Reload token: on every change, refresh the cache for expanded paths so
  // file events (SSE) keep the tree in sync.
  useEffect(() => {
    if (reloadToken === 0) return
    for (const key of expanded) {
      const sep = key.indexOf('::')
      if (sep < 0) continue
      const rootID = key.slice(0, sep)
      const path = key.slice(sep + 2)
      void fetchInto(rootID, path)
    }
    // expanded ist absichtlich keine dep — der token-bump triggert den reload.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reloadToken])

  // Beim Mount oder Routenwechsel den Tree bis zum aktiven Pfad aufklappen
  // und die jeweiligen Inhalte laden, damit nach Reload der aktuelle Ordner
  // sichtbar markiert ist.
  useEffect(() => {
    if (!activeRootID) return
    const paths = ancestorPaths(activePath)
    setExpanded((prev) => {
      const next = new Set(prev)
      for (const p of paths) next.add(cacheKey(activeRootID, p))
      return next
    })
    for (const p of paths) void ensureLoaded(activeRootID, p)
  }, [activeRootID, activePath, ensureLoaded])

  const toggle = useCallback((rootID: string, path: string) => {
    const key = cacheKey(rootID, path)
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(key)) {
        next.delete(key)
      } else {
        next.add(key)
        void ensureLoaded(rootID, path)
      }
      return next
    })
  }, [ensureLoaded])

  // Headers only appear when there is more than one section to separate —
  // i.e. when shared mounts exist. A plain root-only tree stays flat.
  const showSections = dirMounts.length > 0

  const node = (rootID: string, rootName: string) => (
    <TreeNode
      key={rootID}
      rootID={rootID}
      rootName={rootName}
      label={rootName}
      path="/"
      depth={0}
      expanded={expanded}
      files={files}
      loading={loading}
      activeRootID={activeRootID}
      activePath={activePath}
      onToggle={toggle}
      onNavigate={onNavigate}
      expandLabel={t('files.treeExpand', { name: rootName })}
      dnd={dnd}
    />
  )

  return (
    <ul role="tree" className="text-sm py-1">
      {showSections && <SectionHeader label={t('files.sectionOwn')} />}
      {roots.map((root) => node(root.id, root.name))}
      {showSections && <SectionHeader label={t('files.sectionShared')} />}
      {dirMounts.map((m) => node(m.id, m.name))}
    </ul>
  )
}

// Non-interactive section divider between own roots and shared mounts.
// role="presentation" keeps it out of the tree's treeitem semantics.
function SectionHeader({ label }: { label: string }) {
  return (
    <li role="presentation" className="px-3 pt-2 pb-1 text-xs font-medium text-muted-foreground">
      {label}
    </li>
  )
}

interface TreeNodeProps {
  rootID: string
  rootName: string
  label: string
  path: string
  depth: number
  expanded: Set<string>
  files: Map<string, FileEntry[]>
  loading: Set<string>
  activeRootID: string | null
  activePath: string
  onToggle: (rootID: string, path: string) => void
  onNavigate: (rootID: string, path: string) => void
  expandLabel: string
  dnd?: Pick<FilesDnd, 'dropProps' | 'isDropActive'>
}

function TreeNode(props: TreeNodeProps) {
  const {
    rootID, label, path, depth,
    expanded, files, loading,
    activeRootID, activePath,
    onToggle, onNavigate, expandLabel, dnd,
  } = props
  const { t } = useTranslation()
  const key = cacheKey(rootID, path)
  const isExpanded = expanded.has(key)
  const isLoading = loading.has(key)
  const childFiles = files.get(key)
  const childDirs = childFiles ? directSubdirs(childFiles, path) : []
  const isActive = activeRootID === rootID && activePath === path
  // Only nodes of the active root accept a move drop (single-root move API).
  const dropTarget = dnd && rootID === activeRootID
  const dropActive = dropTarget && dnd.isDropActive(path)

  return (
    <li role="treeitem" aria-expanded={isExpanded}>
      <div
        {...(dropTarget ? dnd.dropProps(path) : {})}
        className={cn(
          'flex items-center gap-1 mx-1.5 px-2 py-1 cursor-pointer hover:bg-accent/40 rounded-sm',
          isActive && 'bg-primary/15 text-primary font-medium ring-1 ring-primary/30',
          dropActive && 'bg-primary/20 ring-1 ring-primary/60',
        )}
        style={{ paddingLeft: 8 + depth * 12 }}
        aria-current={isActive ? 'location' : undefined}
      >
        <button
          type="button"
          aria-label={expandLabel}
          onClick={(e) => { e.stopPropagation(); onToggle(rootID, path) }}
          className="flex h-4 w-4 items-center justify-center text-muted-foreground hover:text-foreground"
        >
          {isLoading
            ? <Loader2 className="h-3 w-3 animate-spin" />
            : <ChevronRight className={cn('h-3 w-3 transition-transform', isExpanded && 'rotate-90')} />}
        </button>
        <button
          type="button"
          onClick={() => onNavigate(rootID, path)}
          className="flex items-center gap-1.5 min-w-0 flex-1 text-left"
        >
          <Folder className="h-3.5 w-3.5 text-blue-500 shrink-0" />
          <span className="truncate" title={label}>{label}</span>
        </button>
      </div>
      {isExpanded && childDirs.length > 0 && (
        <ul role="group">
          {childDirs.map((name) => {
            const childPath = path === '/' ? '/' + name : path + '/' + name
            return (
              <TreeNode
                {...props}
                key={childPath}
                label={name}
                path={childPath}
                depth={depth + 1}
                expandLabel={t('files.treeExpand', { name })}
              />
            )
          })}
        </ul>
      )}
    </li>
  )
}
