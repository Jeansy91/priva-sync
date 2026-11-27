import { useState } from 'react'
import { ChevronDown, ChevronRight, Folder } from 'lucide-react'
import { Checkbox, cn } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import type { SyncRoot, PathShareInfo } from '@privasync/api-client'
import { ShareBadge } from './ShareBadge'
import { RowThumb } from './RowThumb'
import { entryRowContextProps, isTouchClick } from './entryRowContext'
import { type DirEntry, isSelectedEntry, buildDirSelection, thumbHostFor } from './helpers'
import type { DetailSelection } from './FileDetailPanel'
import { entryKey, type FilesViewCallbacks } from './filesViewTypes'
import type { TableSection } from './FilesTableView'

// ADR-0012: Grid-Variante der vereinheitlichten Listing-View. Akzeptiert
// dieselbe sections-/entries-Form wie FilesTableView; Section-Header
// werden als Vollbreiten-Banner zwischen die Kachel-Reihen gezogen.

type Props = Pick<FilesViewCallbacks,
  'onSelect' | 'onActivateDir' | 'onActivateFile' | 'onContextMenu' | 'multiselect' | 'dnd'
> & {
  entries?:     DirEntry[]
  sections?:    TableSection[]
  selection:    DetailSelection | null
  // Optional: only needed for file thumbnails. Root listings render dir-only
  // entries (sync-roots) and pass it as undefined.
  selectedRoot?: SyncRoot
  // True while browsing inside a Shared Mount: selectedRoot is the share
  // facade, so thumbnails route through the mount endpoint. See ADR-0012.
  mountMode?:   boolean
  // Geber-side share map. See ADR-0010.
  sharesByPath?: Record<string, PathShareInfo[]>
}

export function FilesGridView({
  entries, sections, selection, selectedRoot, mountMode, sharesByPath,
  onSelect, onActivateDir, onActivateFile, onContextMenu, multiselect, dnd,
}: Props) {
  const effectiveSections: TableSection[] = sections ?? [
    { id: '_flat', entries: entries ?? [] },
  ]
  return (
    <div className="flex flex-col">
      {effectiveSections.map((section) => (
        <GridSection
          key={section.id}
          section={section}
          showHeader={Boolean(section.header)}
          selection={selection}
          selectedRoot={selectedRoot}
          mountMode={mountMode}
          sharesByPath={sharesByPath}
          onSelect={onSelect}
          onActivateDir={onActivateDir}
          onActivateFile={onActivateFile}
          onContextMenu={onContextMenu}
          multiselect={multiselect}
          dnd={dnd}
        />
      ))}
    </div>
  )
}

function GridSection({
  section, showHeader,
  selection, selectedRoot, mountMode, sharesByPath,
  onSelect, onActivateDir, onActivateFile, onContextMenu, multiselect, dnd,
}: {
  section:    TableSection
  showHeader: boolean
} & Omit<Props, 'entries' | 'sections'>) {
  const { t } = useTranslation()
  const [collapsed, setCollapsed] = useState(false)
  return (
    <div>
      {showHeader && (
        <button
          type="button"
          onClick={() => setCollapsed((p) => !p)}
          aria-expanded={!collapsed}
          className="sticky top-0 z-5 flex w-full items-center gap-2 bg-muted/95 px-4 py-2 backdrop-blur-sm shadow-[inset_0_-1px_0_hsl(var(--border))]"
        >
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
        </button>
      )}
      {!collapsed && (
        <div
          data-testid="files-grid"
          className="grid grid-cols-[repeat(auto-fill,minmax(140px,1fr))] gap-3 p-4"
        >
          {section.entries.map((entry) => (
            <Tile
              key={entryKey(entry)}
              entry={entry}
              selection={selection}
              selectedRoot={selectedRoot}
              mountMode={mountMode}
              sharesByPath={sharesByPath}
              onSelect={onSelect}
              onActivateDir={onActivateDir}
              onActivateFile={onActivateFile}
              onContextMenu={onContextMenu}
              multiselect={multiselect}
              dnd={dnd}
            />
          ))}
        </div>
      )}
      {!collapsed && section.footer && (
        <div className="px-4 py-3 text-center">
          {section.footer}
        </div>
      )}
    </div>
  )
}

function Tile({
  entry, selection, selectedRoot, mountMode, sharesByPath,
  onSelect, onActivateDir, onActivateFile, onContextMenu, multiselect, dnd,
}: {
  entry: DirEntry
} & Omit<Props, 'entries' | 'sections'>) {
  const { t } = useTranslation()
  const ekey    = entryKey(entry)
  const checked = multiselect ? multiselect.selected.has(ekey) : false
  // Folder tiles double as drop targets; any tile is a drag source.
  const dropTarget = dnd && entry.kind === 'dir'
  const dropActive = dropTarget && dnd.isDropActive(entry.path)
  // RowThumb dispatcht ueber entry.mount?.id bzw. mountMode, sodass Mounts
  // ihre Thumbnails ueber den Mount-Endpoint laden.
  const thumbHost = thumbHostFor(entry, selectedRoot, mountMode)
  const entryPath = entry.kind === 'file' ? entry.file.path : entry.path
  const activate = () => {
    if (entry.kind === 'dir') onActivateDir(entry.path)
    else onActivateFile(entry)
  }
  const shares    = sharesByPath?.[entryPath] ?? []
  return (
    <button
      type="button"
      data-entry-key={ekey}
      {...(dnd ? dnd.dragProps(entry) : {})}
      {...(dropTarget ? dnd.dropProps(entry.path) : {})}
      onClick={(e) => {
        if (isTouchClick(e)) {
          if (multiselect?.anySelected) multiselect.onCheckboxClick(entry, e)
          else activate()
          return
        }
        if (multiselect) {
          multiselect.onRowClick(entry, e)
          return
        }
        if (entry.kind === 'file') onSelect({ kind: 'file', file: entry.file, mount: entry.mount })
        else onSelect({ ...buildDirSelection(entry.path, entry.name), mount: entry.mount })
      }}
      onDoubleClick={activate}
      {...entryRowContextProps(entry, onContextMenu)}
      className={cn(
        'group select-none flex flex-col items-center gap-2 rounded-md border border-transparent p-2 text-left hover:bg-accent/70 focus-visible:border-border focus-visible:outline-hidden',
        (isSelectedEntry(selection, entry) || checked) && 'bg-accent/40 shadow-[inset_0_0_0_2px_hsl(var(--primary)/0.4)] hover:shadow-[inset_0_0_0_2px_hsl(var(--primary)/0.7)]',
        dropActive && 'bg-primary/15 shadow-[inset_0_0_0_2px_hsl(var(--primary)/0.7)]',
      )}
    >
      <div className="relative flex h-24 w-full items-center justify-center">
        {thumbHost ? (
          <RowThumb {...thumbHost} name={entry.name} size="lg" />
        ) : (
          <Folder className="h-14 w-14 text-blue-500" />
        )}
        {shares.length > 0 && (
          <span
            className="absolute right-0 top-0 rounded-sm bg-background/80 px-0.5 py-0.5"
            onClick={(e) => e.stopPropagation()}
            onDoubleClick={(e) => e.stopPropagation()}
          >
            <ShareBadge shares={shares} href={'/shares?path=' + encodeURIComponent(entryPath)} />
          </span>
        )}
        {multiselect?.showCheckboxes && (
          <span
            className={cn(
              'absolute left-0 top-0 transition-opacity',
              checked ? 'opacity-100' : 'can-hover:opacity-0 group-hover:opacity-100 focus-within:opacity-100',
            )}
            onClick={(e) => e.stopPropagation()}
            onDoubleClick={(e) => e.stopPropagation()}
          >
            <Checkbox
              aria-label={t('files.selectRow', { name: entry.name })}
              checked={checked}
              onChange={() => { /* click drives state */ }}
              onClick={(e) => multiselect.onCheckboxClick(entry, e)}
            />
          </span>
        )}
      </div>
      <span
        className="flex w-full items-center justify-center gap-1 wrap-break-word text-center text-xs leading-tight"
        title={entry.name}
      >
        <span className="truncate">{entry.name}</span>
      </span>
    </button>
  )
}
