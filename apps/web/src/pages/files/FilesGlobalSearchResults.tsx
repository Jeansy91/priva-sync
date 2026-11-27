import { useMemo } from 'react'
import { AlertCircle, Loader2, Search } from 'lucide-react'
import { Button } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import type { FileEntry, SharedMount, SyncRoot } from '@privasync/api-client'
import { FilesTableView, type TableSection } from './FilesTableView'
import type { DirEntry } from './helpers'
import type { FilesViewSort } from './filesViewTypes'

// ADR-0012: Globale Suche kollabiert auf die gemeinsame
// FilesTableView. Diese Komponente baut nur noch die
// Section-Daten + die Wrapper fuer Loading/Error/Empty —
// das eigentliche Rendering, Sticky-Header und Collapsible-
// Verhalten kommen aus FilesTableView.

export type GlobalSearchGroup = {
  rootID:     string
  rootName:   string
  // Set when this group originates from a Shared Mount. Paths inside
  // `files` are already mount-relative (server strips the giver-root
  // prefix). Activation navigates via /files/_m/{mountID}/...
  // See ADR-0010.
  mountID?:   string
  mount?:     SharedMount
  files:      FileEntry[]
  nextCursor: string
  loading:    boolean
}

type Props = {
  groups:        GlobalSearchGroup[]
  loading:       boolean
  error:         string | null
  query:         string
  localeTag:     string
  onActivateFile: (root: SyncRoot, file: FileEntry) => void
  onActivateDir:  (root: SyncRoot, path: string) => void
  onActivateMountFile?: (mountID: string, file: FileEntry) => void
  onActivateMountDir?:  (mountID: string, path: string) => void
  onLoadMore:    (rootID: string) => void
  // Caller resolves rootID -> SyncRoot for navigation.
  rootByID:      Map<string, SyncRoot>
}

function basename(p: string): string {
  const i = p.lastIndexOf('/')
  return i < 0 ? p : p.slice(i + 1)
}

function fileEntry(f: FileEntry, mount: SharedMount | undefined): DirEntry {
  const name = basename(f.path) || f.path
  if (f.is_dir) {
    return { kind: 'dir', name, path: f.path, mount }
  }
  return { kind: 'file', name, file: f, mount }
}

export function FilesGlobalSearchResults({
  groups, loading, error, query, localeTag,
  onActivateFile, onActivateDir,
  onActivateMountFile, onActivateMountDir,
  onLoadMore, rootByID,
}: Props) {
  const { t } = useTranslation()

  // ID-based dispatch table: jeder DirEntry traegt zwar entry.mount,
  // aber der zugrundeliegende Root muss ueber den fileID-Lookup
  // wieder gefunden werden (entries laufen sonst nicht durch das
  // gemeinsame FilesTableView).
  const groupForFile = useMemo(() => {
    const m = new Map<string, GlobalSearchGroup>()
    for (const g of groups) for (const f of g.files) m.set(f.id, g)
    return m
  }, [groups])

  const sections = useMemo<TableSection[]>(() => groups.map((g) => ({
    id:      g.rootID,
    header:  (
      <>
        <span className="truncate">{g.rootName}</span>
        <span className="text-xs text-muted-foreground">
          {g.files.length}{g.nextCursor ? '+' : ''}
        </span>
      </>
    ),
    entries: g.files.map((f) => fileEntry(f, g.mount)),
    footer:  g.nextCursor ? (
      <Button
        size="sm"
        variant="outline"
        onClick={() => onLoadMore(g.rootID)}
        disabled={g.loading}
      >
        {g.loading
          ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
          : t('files.searchLoadMore')}
      </Button>
    ) : null,
  })), [groups, onLoadMore, t])

  // Sort gibt es nicht — die Server-Reihenfolge bleibt erhalten.
  // FilesTableView braucht trotzdem ein FilesViewSort, also einen
  // statischen Stub.
  const noSort: FilesViewSort = useMemo(() => ({
    sortKey:    null,
    sortDir:    null,
    toggleSort: () => {},
    setSort:    () => {},
  }), [])

  if (loading && groups.length === 0) {
    return (
      <div className="flex items-center justify-center py-20 gap-2 text-muted-foreground">
        <Loader2 className="h-5 w-5 animate-spin" />
        <span className="text-sm">{t('files.loading')}</span>
      </div>
    )
  }
  if (error) {
    return (
      <div className="mx-4 mt-3 flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
        <AlertCircle className="h-4 w-4 shrink-0" />
        {error}
      </div>
    )
  }
  if (groups.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-20 gap-3 text-center px-4">
        <Search className="h-10 w-10 text-muted-foreground" />
        <div>
          <p className="text-sm font-medium">{t('files.searchEmptyTitle')}</p>
          <p className="text-xs text-muted-foreground mt-1">
            {t('files.searchGroupEmpty', { query })}
          </p>
        </div>
      </div>
    )
  }

  return (
    <FilesTableView
      sections={sections}
      selection={null}
      localeTag={localeTag}
      sort={noSort}
      onSelect={() => {}}
      onActivateDir={(path) => {
        // Path-only lookup: jeder Treffer-Path ist eindeutig pro
        // Root, die Decoration entry.mount fehlt hier nicht. Wir
        // suchen den Treffer ueber path + filterung is_dir=true.
        for (const g of groups) {
          const hit = g.files.find((f) => f.is_dir && f.path === path)
          if (!hit) continue
          if (g.mountID) onActivateMountDir?.(g.mountID, path)
          else {
            const root = rootByID.get(g.rootID)
            if (root) onActivateDir(root, path)
          }
          return
        }
      }}
      onActivateFile={(entry) => {
        if (entry.kind !== 'file') return
        const g = groupForFile.get(entry.file.id)
        if (!g) return
        if (g.mountID) onActivateMountFile?.(g.mountID, entry.file)
        else {
          const root = rootByID.get(g.rootID)
          if (root) onActivateFile(root, entry.file)
        }
      }}
      entryCallbacks={{ onPreview: () => {} }}
    />
  )
}
