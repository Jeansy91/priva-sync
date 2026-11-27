import { useRef } from 'react'
import { Button, Checkbox, FileIcon } from '@privasync/ui'
import { RotateCcw, Trash2, Loader2 } from 'lucide-react'
import { useTranslation } from '@privasync/i18n'
import { TitleWrap } from '../../components/TitleWrap'
import { SortableTh } from '../../components/SortableTh'
import { HeaderCheckbox } from './TrashTree'
import { fileName, dirPath, formatBytes, formatDate, type TrashEntryWithRoot } from './trashHelpers'
import type { useTableSort } from '../../hooks/useTableSort'

interface TrashListTableProps {
  entries:        TrashEntryWithRoot[]
  sortedEntries:  TrashEntryWithRoot[]
  trashSort:      ReturnType<typeof useTableSort<TrashEntryWithRoot, 'file' | 'size' | 'deletedAt'>>
  selectedIds:    Set<string>
  setSelectedIds: React.Dispatch<React.SetStateAction<Set<string>>>
  localeTag:      string
  restoring:      string | null
  purging:        string | null
  purgingAll:     boolean
  onRestore:      (entry: TrashEntryWithRoot) => void
  onPurgeOne:     (entry: TrashEntryWithRoot) => void
}

/**
 * Flat-list view of trashed files with sortable columns. The checkbox
 * column supports range-select via Shift+click anchored at the last
 * toggled row.
 */
export function TrashListTable(props: TrashListTableProps) {
  const {
    entries, sortedEntries, trashSort, selectedIds, setSelectedIds, localeTag,
    restoring, purging, purgingAll, onRestore, onPurgeOne,
  } = props
  const { t } = useTranslation()
  const shiftDownRef = useRef(false)
  const anchorIdRef  = useRef<string | null>(null)

  return (
    <div className="relative rounded-lg border border-border overflow-x-auto">
      <table className="w-full text-sm">
        <thead className="sticky top-0 z-10 bg-muted/95 backdrop-blur-sm">
          <tr className="border-b border-border text-muted-foreground">
            <th className="px-3 py-2.5 w-8">
              <HeaderCheckbox
                total={entries.length}
                selectedCount={selectedIds.size}
                label={t('trash.selectAll')}
                onToggle={(check) => {
                  setSelectedIds(check ? new Set(entries.map((e) => e.id)) : new Set())
                }}
              />
            </th>
            <SortableTh
              className="text-left px-4 py-2.5 font-medium w-full"
              label={t('trash.colFile')}
              active={trashSort.sortKey === 'file'}
              dir={trashSort.sortDir}
              onToggle={() => trashSort.toggleSort('file')}
            />
            <SortableTh
              className="text-right px-4 py-2.5 font-medium whitespace-nowrap hidden sm:table-cell"
              label={t('trash.colSize')}
              active={trashSort.sortKey === 'size'}
              dir={trashSort.sortDir}
              onToggle={() => trashSort.toggleSort('size')}
              align="right"
            />
            <SortableTh
              className="text-right px-4 py-2.5 font-medium whitespace-nowrap hidden md:table-cell"
              label={t('trash.colDeletedAt')}
              active={trashSort.sortKey === 'deletedAt'}
              dir={trashSort.sortDir}
              onToggle={() => trashSort.toggleSort('deletedAt')}
              align="right"
            />
            <th className="px-3 py-2.5 w-8" />
          </tr>
        </thead>
        <tbody>
          {sortedEntries.map((entry) => (
            <tr
              key={entry.id}
              className="border-b border-border/50 last:border-0 hover:bg-accent/20 transition-colors"
            >
              <td className="px-3 py-2.5 w-8">
                <Checkbox
                  aria-label={fileName(entry.path)}
                  checked={selectedIds.has(entry.id)}
                  onMouseDown={(e) => { shiftDownRef.current = e.shiftKey }}
                  onKeyDown={(e) => { shiftDownRef.current = e.shiftKey }}
                  onChange={(e) => {
                    const shift = shiftDownRef.current
                    shiftDownRef.current = false
                    const anchorId = anchorIdRef.current
                    if (shift && anchorId) {
                      const ids = entries.map((x) => x.id)
                      const a = ids.indexOf(anchorId)
                      const b = ids.indexOf(entry.id)
                      if (a >= 0 && b >= 0) {
                        const [lo, hi] = a < b ? [a, b] : [b, a]
                        setSelectedIds((prev) => {
                          const next = new Set(prev)
                          for (let i = lo; i <= hi; i++) next.add(ids[i])
                          return next
                        })
                        return
                      }
                    }
                    setSelectedIds((prev) => {
                      const next = new Set(prev)
                      if (e.target.checked) next.add(entry.id)
                      else next.delete(entry.id)
                      return next
                    })
                    anchorIdRef.current = entry.id
                  }}
                />
              </td>
              <td className="px-4 py-2.5 max-w-0">
                <div className="flex items-center gap-2.5 min-w-0">
                  <FileIcon name={fileName(entry.path)} className="h-4 w-4" />
                  <div className="min-w-0">
                    <p className="truncate font-medium">{fileName(entry.path)}</p>
                    <p className="text-xs text-muted-foreground truncate">
                      {entry.rootName}{dirPath(entry.path)}
                    </p>
                  </div>
                </div>
              </td>
              <td className="px-4 py-2.5 text-right text-muted-foreground whitespace-nowrap hidden sm:table-cell">
                {formatBytes(entry.size)}
              </td>
              <td className="px-4 py-2.5 text-right text-muted-foreground whitespace-nowrap hidden md:table-cell">
                {formatDate(entry.deleted_at, localeTag)}
              </td>
              <td className="px-3 py-2 text-right">
                <div className="flex items-center justify-end gap-1">
                  <TitleWrap title={t('trash.restore')}>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7"
                      onClick={() => onRestore(entry)}
                      disabled={restoring === entry.id || purging === entry.id || purgingAll}
                      aria-label={t('trash.restore')}
                    >
                      {restoring === entry.id
                        ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        : <RotateCcw className="h-3.5 w-3.5" />}
                    </Button>
                  </TitleWrap>
                  <TitleWrap title={t('trash.purgeOne')}>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7 text-destructive hover:text-destructive hover:bg-destructive/10"
                      onClick={() => onPurgeOne(entry)}
                      disabled={restoring === entry.id || purging === entry.id || purgingAll}
                      aria-label={t('trash.purgeOne')}
                    >
                      {purging === entry.id
                        ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        : <Trash2 className="h-3.5 w-3.5" />}
                    </Button>
                  </TitleWrap>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
