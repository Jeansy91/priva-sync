import { useState, type ReactNode } from 'react'
import { ChevronDown, ChevronRight, Folder } from 'lucide-react'
import { Checkbox, cn } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import type { SyncRoot, PathShareInfo } from '@privasync/api-client'
import { ShareBadge } from './ShareBadge'
import { MountAccessIcon } from './MountAccessIcon'
import { SortableTh } from '../../components/SortableTh'
import { useMediaQuery } from '../../hooks/useMediaQuery'
import { EntryMenu } from './EntryMenu'
import { entryRowContextProps, isTouchClick } from './entryRowContext'
import { RowThumb } from './RowThumb'
import {
  type DirEntry, formatBytes, formatDate, isSelectedEntry, buildDirSelection, thumbHostFor,
} from './helpers'
import type { DetailSelection } from './FileDetailPanel'
import { entryKey, type FilesViewCallbacks, type FilesViewSort } from './filesViewTypes'

// ADR-0012: Sections lets the same table render grouped listings (root
// list with "Eigene" / "Mit mir geteilt", or global search per root)
// alongside the flat in-root variant. A flat caller passes `entries`;
// a grouped caller passes `sections` with one entry per group.
export type TableSection = {
  id:       string
  header?:  ReactNode
  entries:  DirEntry[]
  // Optional footer-row rendered after the entries (e.g. "Load more"
  // for a paginated search-result group). Receives the same colspan as
  // the header.
  footer?:  ReactNode
}

type Props = FilesViewCallbacks & {
  entries?:     DirEntry[]
  sections?:    TableSection[]
  selection:    DetailSelection | null
  // Optional: only needed for file thumbnails. Root listings render dir-only
  // entries (sync-roots) and pass it as undefined.
  selectedRoot?: SyncRoot
  // True while browsing inside a Shared Mount: selectedRoot is the share
  // facade, so thumbnails route through the mount endpoint. See ADR-0012.
  mountMode?:   boolean
  localeTag:    string
  sort:         FilesViewSort
  // Geber-side share map. Each entry whose path matches a key gets a
  // badge next to its name. See ADR-0010.
  sharesByPath?: Record<string, PathShareInfo[]>
}

export function FilesTableView({
  entries, sections, selection, selectedRoot, mountMode, localeTag, sort,
  onSelect, onActivateDir, onActivateFile,
  entryCallbacks, onContextMenu, multiselect, dnd,
  sharesByPath,
}: Props) {
  const { t } = useTranslation()
  const effectiveSections: TableSection[] = sections ?? [
    { id: '_flat', entries: entries ?? [] },
  ]
  // Spanning rows (section header, footer) must span exactly the visible
  // columns: under table-fixed, a colSpan wider than the rendered columns
  // makes the browser invent phantom columns that steal width from the
  // name column. Breakpoints mirror the hidden/*:table-cell classes below.
  const sm = useMediaQuery('(min-width: 640px)', true)
  const md = useMediaQuery('(min-width: 768px)', true)
  const lg = useMediaQuery('(min-width: 1024px)', true)
  const colSpan = (multiselect?.showCheckboxes ? 1 : 0)
    + 2                // name, menu
    + (sm ? 1 : 0)     // size
    + (md ? 2 : 0)     // user + public share
    + (lg ? 1 : 0)     // mtime
  return (
    // Fixed layout: side columns get explicit widths and the name column takes
    // the rest, so long names truncate instead of widening the table. The
    // per-breakpoint min-width keeps ~12rem for the name once side columns
    // appear; below that the scroll host scrolls horizontally.
    <table className="w-full table-fixed text-sm sm:min-w-[24rem] md:min-w-120 lg:min-w-160">
      {/* Background on the sticky element itself, not on its row — the
          pattern every other table uses. With it on the row, AVG Secure
          Browser rendered the header transparent over the first row. */}
      <thead className="sticky top-0 z-10 bg-background shadow-[inset_0_-1px_0_hsl(var(--border))]">
        <tr className="text-muted-foreground">
          {multiselect?.showCheckboxes && (
            <th className="px-2 py-2 w-8 align-middle">
              <div className="flex items-center justify-center">
                <Checkbox
                  aria-label={t('files.selectAll')}
                  checked={multiselect.allSelected}
                  indeterminate={!multiselect.allSelected && multiselect.anySelected}
                  onChange={multiselect.onToggleAll}
                  onClick={(e) => e.stopPropagation()}
                />
              </div>
            </th>
          )}
          <SortableTh
            className="text-left px-4 py-2 font-medium"
            label={t('files.colName')}
            active={sort.sortKey === 'name'}
            dir={sort.sortDir}
            onToggle={() => sort.toggleSort('name')}
          />
          {/* Two empty share columns — header stays blank, only the body
              cells carry the per-row share icons. */}
          <th className="px-2 py-2 w-10 hidden md:table-cell" aria-label={t('files.colSharedUser')} />
          <th className="px-2 py-2 w-10 hidden md:table-cell" aria-label={t('files.colSharedPublic')} />
          <SortableTh
            className="text-right px-4 py-2 font-medium whitespace-nowrap hidden sm:table-cell w-28"
            label={t('files.colSize')}
            active={sort.sortKey === 'size'}
            dir={sort.sortDir}
            onToggle={() => sort.toggleSort('size')}
            align="right"
          />
          <SortableTh
            className="text-right px-4 py-2 font-medium whitespace-nowrap hidden lg:table-cell w-44"
            label={t('files.colMtime')}
            active={sort.sortKey === 'mtime'}
            dir={sort.sortDir}
            onToggle={() => sort.toggleSort('mtime')}
            align="right"
          />
          <th className="px-2 py-2 w-11" />
        </tr>
      </thead>
      {effectiveSections.map((section) => (
        <SectionBody
          key={section.id}
          section={section}
          colSpan={colSpan}
          showHeader={Boolean(section.header)}
          selection={selection}
          selectedRoot={selectedRoot}
          mountMode={mountMode}
          localeTag={localeTag}
          onSelect={onSelect}
          onActivateDir={onActivateDir}
          onActivateFile={onActivateFile}
          entryCallbacks={entryCallbacks}
          onContextMenu={onContextMenu}
          multiselect={multiselect}
          dnd={dnd}
          sharesByPath={sharesByPath}
        />
      ))}
    </table>
  )
}

function SectionBody({
  section, colSpan, showHeader,
  selection, selectedRoot, mountMode, localeTag,
  onSelect, onActivateDir, onActivateFile,
  entryCallbacks, onContextMenu, multiselect, dnd, sharesByPath,
}: {
  section:      TableSection
  colSpan:      number
  showHeader:   boolean
} & Omit<Props, 'entries' | 'sections' | 'sort'>) {
  const { t } = useTranslation()
  const [collapsed, setCollapsed] = useState(false)
  return (
    <tbody className="border-b border-border/50">
      {showHeader && (
        <tr
          className="cursor-pointer hover:bg-accent/30 transition-colors"
          onClick={() => setCollapsed((p) => !p)}
          aria-expanded={!collapsed}
        >
          <td
            colSpan={colSpan}
            className="sticky top-9 z-5 bg-muted/95 backdrop-blur-sm px-4 py-2 shadow-[inset_0_-1px_0_hsl(var(--border))]"
          >
            <div className="flex items-center gap-2">
              {collapsed
                ? <ChevronRight className="h-4 w-4 text-muted-foreground shrink-0" />
                : <ChevronDown  className="h-4 w-4 text-muted-foreground shrink-0" />}
              <span className="text-xs font-semibold uppercase tracking-wide">
                {section.header}
              </span>
              <span className="text-xs text-muted-foreground">{section.entries.length}</span>
              <span className="sr-only">
                {collapsed ? t('files.searchGroupExpand') : t('files.searchGroupCollapse')}
              </span>
            </div>
          </td>
        </tr>
      )}
      {!collapsed && section.entries.map((entry) => (
        <Row
          key={entryKey(entry)}
          entry={entry}
          selection={selection}
          selectedRoot={selectedRoot}
          mountMode={mountMode}
          localeTag={localeTag}
          onSelect={onSelect}
          onActivateDir={onActivateDir}
          onActivateFile={onActivateFile}
          entryCallbacks={entryCallbacks}
          onContextMenu={onContextMenu}
          multiselect={multiselect}
          dnd={dnd}
          sharesByPath={sharesByPath}
        />
      ))}
      {!collapsed && section.footer && (
        <tr>
          <td colSpan={colSpan} className="px-4 py-3 text-center">
            {section.footer}
          </td>
        </tr>
      )}
    </tbody>
  )
}

function Row({
  entry, selection, selectedRoot, mountMode, localeTag,
  onSelect, onActivateDir, onActivateFile,
  entryCallbacks, onContextMenu, multiselect, dnd, sharesByPath,
}: {
  entry: DirEntry
} & Omit<Props, 'entries' | 'sections' | 'sort'>) {
  const { t } = useTranslation()
  const ekey       = entryKey(entry)
  const checked    = multiselect ? multiselect.selected.has(ekey) : false
  const ms         = multiselect
  const baseSelect = isSelectedEntry(selection, entry)
  // A folder is a drop target; every entry is a drag source. Mount entries in
  // the in-root listing carry no `mount`, so dnd applies uniformly here.
  const dropTarget = dnd && entry.kind === 'dir'
  const dropActive = dropTarget && dnd.isDropActive(entry.path)
  // RowThumb dispatcht ueber entry.mount?.id (Mount-Endpoint) vs.
  // selectedRoot.id (Sync-Root-Endpoint). Dir-Eintraege bekommen das
  // Folder-Icon, File-Mounts und eigene Files den Thumb.
  const thumbHost = thumbHostFor(entry, selectedRoot, mountMode)
  // Split the path's shares by type so each share-kind lands in its own
  // column; ShareBadge renders only the icon matching the shares it gets.
  const entryPath   = entry.kind === 'file' ? entry.file.path : entry.path
  const shareList   = sharesByPath?.[entryPath] ?? []
  const userShares  = shareList.filter((s) => s.share_type === 'user')
  const publicShares = shareList.filter((s) => s.share_type === 'public')
  const shareHref   = '/shares?path=' + encodeURIComponent(entryPath)
  const activate = () => {
    if (entry.kind === 'dir') onActivateDir(entry.path)
    else onActivateFile(entry)
  }
  return (
    <tr
      tabIndex={0}
      data-entry-key={ekey}
      {...(dnd ? dnd.dragProps(entry) : {})}
      {...(dropTarget ? dnd.dropProps(entry.path) : {})}
      className={cn(
        'group select-none border-b border-border/50 hover:bg-accent/70 transition-colors cursor-pointer focus:outline-hidden focus-visible:bg-accent/70',
        (baseSelect || checked) && 'bg-accent/40 rounded-md shadow-[inset_0_0_0_2px_hsl(var(--primary)/0.25)] hover:shadow-[inset_0_0_0_2px_hsl(var(--primary)/0.5)]',
        dropActive && 'bg-primary/15 shadow-[inset_0_0_0_2px_hsl(var(--primary)/0.7)]',
      )}
      onClick={(e) => {
        if (isTouchClick(e)) {
          if (ms?.anySelected) ms.onCheckboxClick(entry, e)
          else activate()
          return
        }
        if (ms) {
          ms.onRowClick(entry, e)
          return
        }
        if (entry.kind === 'file') onSelect({ kind: 'file', file: entry.file, mount: entry.mount })
        else onSelect({ ...buildDirSelection(entry.path, entry.name), mount: entry.mount })
      }}
      onDoubleClick={activate}
      {...entryRowContextProps(entry, onContextMenu)}
    >
      {ms?.showCheckboxes && (
        <td className="px-2 py-2 w-8 align-middle" onClick={(e) => e.stopPropagation()}>
          <div className="flex items-center justify-center">
            <Checkbox
              aria-label={t('files.selectRow', { name: entry.name })}
              checked={checked}
              onChange={() => { /* click drives state */ }}
              onClick={(e) => ms.onCheckboxClick(entry, e)}
            />
          </div>
        </td>
      )}
      <td className="px-4 py-2">
        <div className="flex items-center gap-2.5 min-w-0">
          {thumbHost ? (
            <RowThumb {...thumbHost} name={entry.name} />
          ) : (
            <span className="inline-flex items-center justify-center h-7 w-7 shrink-0">
              <Folder className="h-4 w-4 text-blue-500" />
            </span>
          )}
          <span className="truncate">{entry.name}</span>
        </div>
      </td>
      <td className="px-2 py-2 text-center hidden md:table-cell">
        <div className="flex items-center justify-center">
          {/* Recipient view: a mount shows its access level here; the giver's
              own entries show with-whom-shared. The two never overlap — a
              mount cannot be re-shared. */}
          {entry.mount
            ? <MountAccessIcon canWrite={entry.mount.can_write} />
            : <ShareBadge shares={userShares} href={shareHref} />}
        </div>
      </td>
      <td className="px-2 py-2 text-center hidden md:table-cell">
        <div className="flex items-center justify-center">
          <ShareBadge shares={publicShares} href={shareHref} />
        </div>
      </td>
      <td className="px-4 py-2 text-right text-muted-foreground hidden sm:table-cell whitespace-nowrap">
        {entry.kind === 'file' ? formatBytes(entry.file.size) : '—'}
      </td>
      <td className="px-4 py-2 text-right text-muted-foreground hidden lg:table-cell whitespace-nowrap">
        {entry.kind === 'file' ? formatDate(entry.file.mtime, localeTag) : '—'}
      </td>
      <td
        className="px-2 py-1 text-right"
        onClick={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
      >
        <EntryMenu entry={entry} callbacks={entryCallbacks} />
      </td>
    </tr>
  )
}
