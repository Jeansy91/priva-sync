import { AlertCircle, Folder, Loader2, Search } from 'lucide-react'
import { Button, cn } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import type { SyncRoot } from '@privasync/api-client'
import { EntryMenu } from './EntryMenu'
import { entryRowContextProps, isTouchClick } from './entryRowContext'
import { RowThumb } from './RowThumb'
import { type DirEntry, formatBytes, formatDate, isSelectedEntry } from './helpers'
import type { DetailSelection } from './FileDetailPanel'
import type { SearchHit } from './searchEntries'
import type { FilesViewCallbacks } from './filesViewTypes'

type Props = FilesViewCallbacks & {
  hits:         SearchHit[]
  selection:    DetailSelection | null
  selectedRoot: SyncRoot
  localeTag:    string
  query:        string
  loading:      boolean
  error:        string | null
  nextCursor:   string
  onLoadMore:   () => void
}

export function FilesSearchResults({
  hits, selection, selectedRoot, localeTag, query, loading, error,
  nextCursor, onLoadMore,
  onSelect, onActivateDir, onActivateFile,
  entryCallbacks, onContextMenu,
}: Props) {
  const { t } = useTranslation()

  if (loading && hits.length === 0) {
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
  if (hits.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-20 gap-3 text-center px-4">
        <Search className="h-10 w-10 text-muted-foreground" />
        <div>
          <p className="text-sm font-medium">{t('files.searchEmptyTitle')}</p>
          <p className="text-xs text-muted-foreground mt-1">
            {t('files.searchEmptyHint', { query })}
          </p>
        </div>
      </div>
    )
  }

  return (
    <>
      <table className="w-full text-sm">
        <thead className="sticky top-0 z-10 bg-background shadow-[inset_0_-1px_0_hsl(var(--border))]">
          <tr className="text-muted-foreground">
            <th className="text-left px-4 py-2 font-medium">{t('files.colName')}</th>
            <th className="text-left px-4 py-2 font-medium hidden sm:table-cell">{t('files.colPath')}</th>
            <th className="text-right px-4 py-2 font-medium whitespace-nowrap hidden md:table-cell">{t('files.colSize')}</th>
            <th className="text-right px-4 py-2 font-medium whitespace-nowrap hidden md:table-cell">{t('files.colMtime')}</th>
            <th className="px-2 py-2 w-8" />
          </tr>
        </thead>
        <tbody>
          {hits.map((hit) => {
            const key = hit.kind === 'file' ? `file:${hit.file.id}` : `dir:${hit.path}`
            const entry: DirEntry = hit.kind === 'file'
              ? { kind: 'file', name: hit.name, file: hit.file }
              : { kind: 'dir',  name: hit.name, path: hit.path }
            const activate = () => {
              if (entry.kind === 'dir') onActivateDir(entry.path)
              else onActivateFile(entry)
            }
            return (
              <tr
                key={key}
                tabIndex={0}
                className={cn(
                  'group border-b border-border/50 hover:bg-accent/30 transition-colors cursor-pointer focus:outline-hidden focus-visible:bg-accent/40',
                  isSelectedEntry(selection, entry) && 'bg-accent/40',
                )}
                onClick={(e) => {
                  if (isTouchClick(e)) { activate(); return }
                  if (entry.kind === 'file') onSelect({ kind: 'file', file: entry.file })
                  else onSelect({ kind: 'dir', name: entry.name, path: entry.path, itemCount: 0, totalSize: 0, latestMtime: null })
                }}
                onDoubleClick={activate}
                {...entryRowContextProps(entry, onContextMenu)}
              >
                <td className="px-4 py-2">
                  <div className="flex items-center gap-2.5 min-w-0">
                    {entry.kind === 'dir' ? (
                      <span className="inline-flex items-center justify-center h-7 w-7 shrink-0">
                        <Folder className="h-4 w-4 text-blue-500" />
                      </span>
                    ) : (
                      <RowThumb rootID={selectedRoot.id} path={entry.file.path} name={entry.name} />
                    )}
                    <span className="truncate">{entry.name}</span>
                  </div>
                </td>
                <td className="px-4 py-2 text-muted-foreground hidden sm:table-cell truncate max-w-[20rem]">
                  {entry.kind === 'file' ? entry.file.path : entry.path}
                </td>
                <td className="px-4 py-2 text-right text-muted-foreground hidden md:table-cell whitespace-nowrap">
                  {entry.kind === 'file' ? formatBytes(entry.file.size) : '—'}
                </td>
                <td className="px-4 py-2 text-right text-muted-foreground hidden md:table-cell whitespace-nowrap">
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
          })}
        </tbody>
      </table>
      {nextCursor && (
        <div className="flex justify-center py-4">
          <Button size="sm" variant="outline" onClick={onLoadMore} disabled={loading}>
            {loading
              ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
              : t('files.searchLoadMore')}
          </Button>
        </div>
      )}
    </>
  )
}

