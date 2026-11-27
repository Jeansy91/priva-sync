import type { SortDir } from '../../hooks/useTableSort'
import type { DetailSelection } from './FileDetailPanel'
import type { DirEntry } from './helpers'
import type { EntryMenuCallbacks } from './entryMenuItems'

export type FilesViewMode = 'table' | 'grid'

export type FilesSortKey = 'name' | 'size' | 'mtime'

export type FilesViewSort = {
  sortKey:    FilesSortKey | null
  sortDir:    SortDir | null
  toggleSort: (key: FilesSortKey) => void
  setSort:    (key: FilesSortKey, dir: SortDir) => void
}

export type FilesViewCallbacks = {
  onSelect:       (sel: DetailSelection) => void
  onActivateDir:  (path: string) => void
  onActivateFile: (entry: DirEntry) => void
  // Full menu callbacks — shared between the row's `...`-menu and the
  // right-click context menu so the two can never differ.
  entryCallbacks: EntryMenuCallbacks
  onContextMenu?: (entry: DirEntry, x: number, y: number) => void
  // Multiselect plumbing. Headless: views render checkboxes + handle clicks
  // through these, the hook lives in FilesPage.
  multiselect?: MultiselectViewProps
  // Drag-and-drop move plumbing. Only wired for the in-root listing; the
  // root list and search results leave it undefined.
  dnd?: FilesDnd
}

export type EntryKey = string

export function entryKey(entry: DirEntry): EntryKey {
  return entry.kind === 'dir' ? 'dir:' + entry.path : 'file:' + entry.file.path
}

// Inverse of entryKey: split a selection key back into its kind + path. Shared
// by the bulk-action pipeline and the drag-and-drop mover.
export function keyToPath(k: EntryKey): { kind: 'dir' | 'file'; path: string } {
  if (k.startsWith('dir:')) return { kind: 'dir', path: k.slice(4) }
  return { kind: 'file', path: k.slice(5) }
}

// Headless drag-and-drop move handle. The hook lives in FilesPage and the same
// instance is threaded into the table/grid rows (drag sources + folder drop
// targets), the breadcrumbs, and the tree (drop targets only).
export type FilesDnd = {
  // Spread onto a draggable row/tile. Decides whether the grabbed entry drags
  // alone or together with the whole multi-selection.
  dragProps: (entry: DirEntry) => {
    draggable:   boolean
    onDragStart: (e: React.DragEvent) => void
    onDragEnd:   () => void
  }
  // Spread onto a folder row, breadcrumb segment, or tree node. `targetDir` is
  // the directory the drop would move the dragged entries INTO.
  dropProps: (targetDir: string) => {
    onDragOver:  (e: React.DragEvent) => void
    onDragLeave: (e: React.DragEvent) => void
    onDrop:      (e: React.DragEvent) => void
  }
  // True while a valid drag hovers `targetDir` — drives the drop highlight.
  isDropActive: (targetDir: string) => boolean
}

export type MultiselectViewProps = {
  selected:        Set<EntryKey>
  onRowClick:      (entry: DirEntry, e: React.MouseEvent) => void
  onCheckboxClick: (entry: DirEntry, e: React.MouseEvent) => void
  onToggleAll:     () => void
  allSelected:     boolean
  anySelected:     boolean
  // When false, multi-select behavior stays active (Ctrl/Shift-click, lasso)
  // but the visual checkbox column / hover-overlay is hidden. Set via the
  // per-user `show_file_checkboxes` preference.
  showCheckboxes:  boolean
}
