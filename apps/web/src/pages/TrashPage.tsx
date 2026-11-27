import { useState, useCallback } from 'react'
import { RotateCcw, Trash2, Loader2, AlertCircle, CheckCircle, List, FolderTree } from 'lucide-react'
import { Button } from '@privasync/ui'
import { apiClient, useAuthStore } from '../store/auth'
import { useTranslation } from '@privasync/i18n'
import { useScrolled } from '../hooks/useScrolled'
import { TitleWrap } from '../components/TitleWrap'
import { useTableSort } from '../hooks/useTableSort'
import {
  fileName, formatBytes,
  type TrashEntryWithRoot, type PurgeTarget,
} from './trash/trashHelpers'
import { TrashTree } from './trash/TrashTree'
import { TrashListTable } from './trash/TrashListTable'
import { PurgeDialog } from './trash/PurgeDialog'
import { useTrashData } from './trash/useTrashData'
import { PageHeader } from '../components/PageHeader'

// Re-export for the standalone tree test that imports `./TrashPage`.
export { buildTrashTree } from './trash/trashHelpers'

export default function TrashPage() {
  const { t, i18n } = useTranslation()
  const localeTag = i18n.language === 'de' ? 'de-DE' : 'en-US'
  const { scrolled, onScroll } = useScrolled()
  const { user, setUser } = useAuthStore()

  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [purgeTarget, setPurgeTarget] = useState<PurgeTarget | null>(null)
  const [viewMode, setViewModeLocal] = useState<'list' | 'tree'>(user?.trash_view ?? 'list')

  const {
    syncRoots, entries, loading, error, restoreError,
    restoring, purging, purgingAll, bulkBusy,
    handleRestore, doPurgeOne, doPurgeAll,
    doRestoreEntries, doPurgeEntries,
  } = useTrashData(t, setSelectedIds)

  const trashSort = useTableSort(entries, {
    storageKey: 'trash',
    comparators: {
      file:      (a, b) => fileName(a.path).localeCompare(fileName(b.path)),
      size:      (a, b) => a.size - b.size,
      deletedAt: (a, b) => (a.deleted_at ?? '').localeCompare(b.deleted_at ?? ''),
    },
  })
  const sortedEntries = trashSort.sorted

  // Persist the per-user view preference: optimistic store update plus
  // fire-and-forget API call. Rolls back on failure so UI and server stay in
  // sync.
  const setViewMode = useCallback((next: 'list' | 'tree') => {
    setViewModeLocal(next)
    if (!user || user.trash_view === next) return
    const prev = user.trash_view
    setUser({ ...user, trash_view: next })
    apiClient.auth.setTrashView(next).catch(() => {
      setUser({ ...user, trash_view: prev })
      setViewModeLocal(prev)
    })
  }, [user, setUser])

  const doBulkRestore = () =>
    doRestoreEntries(entries.filter((e) => selectedIds.has(e.id)))

  const doBulkPurge = () =>
    doPurgeEntries(entries.filter((e) => selectedIds.has(e.id)))

  const onPurgeConfirm = async (target: PurgeTarget) => {
    if (target.kind === 'one')      await doPurgeOne(target.entry)
    else if (target.kind === 'all') await doPurgeAll()
    else if (target.kind === 'selected') await doBulkPurge()
    else if (target.kind === 'list')     await doPurgeEntries(target.entries)
  }

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title={t('trash.title')}
        subtitle={t('trash.subtitle')}
        scrolled={scrolled}
        actions={
          <>
            <div className="flex items-center rounded-md border border-border overflow-hidden">
              <TitleWrap title={t('trash.viewList')}>
                <Button
                  variant={viewMode === 'list' ? 'secondary' : 'ghost'}
                  size="icon"
                  className="h-8 w-8 rounded-none"
                  aria-label={t('trash.viewList')}
                  aria-pressed={viewMode === 'list'}
                  onClick={() => setViewMode('list')}
                >
                  <List className="h-3.5 w-3.5" />
                </Button>
              </TitleWrap>
              <TitleWrap title={t('trash.viewTree')}>
                <Button
                  variant={viewMode === 'tree' ? 'secondary' : 'ghost'}
                  size="icon"
                  className="h-8 w-8 rounded-none"
                  aria-label={t('trash.viewTree')}
                  aria-pressed={viewMode === 'tree'}
                  onClick={() => setViewMode('tree')}
                >
                  <FolderTree className="h-3.5 w-3.5" />
                </Button>
              </TitleWrap>
            </div>
            {entries.length > 0 && (
              <TitleWrap title={t('trash.emptyButton')}>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setPurgeTarget({ kind: 'all', count: entries.length })}
                  disabled={purgingAll || loading}
                  className="text-destructive hover:text-destructive"
                >
                  {purgingAll
                    ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    : <Trash2 className="h-3.5 w-3.5" />}
                  {t('trash.emptyButton')}
                </Button>
              </TitleWrap>
            )}
          </>
        }
      />

      <div className="flex-1 overflow-auto" onScroll={onScroll}>
        <div className="max-w-7xl mx-auto p-4 sm:p-6 space-y-6">
      {/* Trash size — counts against the user's main quota; no separate cap.
          Doubles as the selection toolbar when one or more entries are checked.
          Fixed height so the layout does not jump when the selection toggles. */}
      {(selectedIds.size > 0 || (user && user.trash_bytes > 0)) && (
        <div
          {...(selectedIds.size > 0
            ? { role: 'toolbar', 'aria-label': t('trash.bulkToolbar') }
            : {})}
          className="flex items-center justify-between gap-3 rounded-lg border border-border bg-card px-3 py-1.5 h-11"
        >
          <div className="text-xs text-muted-foreground">
            {selectedIds.size > 0 ? (
              <>
                {t('trash.selectedLabel')}:{' '}
                <span className="font-medium text-foreground">
                  {formatBytes(
                    entries.filter((e) => selectedIds.has(e.id)).reduce((s, e) => s + e.size, 0),
                  )}
                </span>
              </>
            ) : (
              <>
                {t('trash.used')}:{' '}
                <span className="font-medium text-foreground">
                  {formatBytes(user!.trash_bytes)}
                </span>
              </>
            )}
          </div>
          {selectedIds.size > 0 && (
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => doBulkRestore()}
                disabled={bulkBusy}
              >
                <RotateCcw className="h-3.5 w-3.5" />
                {t('trash.bulkRestore')}
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="text-destructive hover:text-destructive"
                onClick={() => setPurgeTarget({ kind: 'selected', count: selectedIds.size })}
                disabled={bulkBusy}
              >
                <Trash2 className="h-3.5 w-3.5" />
                {t('trash.bulkPurge')}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setSelectedIds(new Set())}
                disabled={bulkBusy}
              >
                {t('trash.bulkCancel')}
              </Button>
            </div>
          )}
        </div>
      )}

      {error && (
        <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {error}
        </div>
      )}

      {restoreError && (
        <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {restoreError}
        </div>
      )}

      {loading && entries.length === 0 ? (
        <div className="flex items-center justify-center py-20 gap-2 text-muted-foreground">
          <Loader2 className="h-5 w-5 animate-spin" />
          <span className="text-sm">{t('trash.loading')}</span>
        </div>
      ) : entries.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-20 gap-3 text-center">
          <CheckCircle className="h-12 w-12 text-green-500" />
          <div>
            <p className="text-base font-medium">{t('trash.emptyTitle')}</p>
            <p className="text-sm text-muted-foreground mt-1">
              {t('trash.emptyHint')}
            </p>
          </div>
        </div>
      ) : viewMode === 'tree' ? (
        <TrashTree
          roots={syncRoots}
          entries={entries}
          selectedIds={selectedIds}
          setSelectedIds={setSelectedIds}
          localeTag={localeTag}
          labels={{
            colFile:      t('trash.colFile'),
            colSize:      t('trash.colSize'),
            colDeletedAt: t('trash.colDeletedAt'),
            selectAll:    t('trash.selectAll'),
            expand:       t('trash.expand'),
            collapse:     t('trash.collapse'),
            restore:      t('trash.restore'),
            purgeOne:     t('trash.purgeOne'),
          }}
          onRestore={handleRestore}
          onPurge={(entry) => setPurgeTarget({ kind: 'one', entry })}
          onRestoreMany={doRestoreEntries}
          onPurgeMany={(items) => setPurgeTarget({ kind: 'list', entries: items })}
          restoring={restoring}
          purging={purging}
          busy={purgingAll || bulkBusy}
        />
      ) : (
        <TrashListTable
          entries={entries}
          sortedEntries={sortedEntries}
          trashSort={trashSort}
          selectedIds={selectedIds}
          setSelectedIds={setSelectedIds}
          localeTag={localeTag}
          restoring={restoring}
          purging={purging}
          purgingAll={purgingAll}
          onRestore={handleRestore}
          onPurgeOne={(entry) => setPurgeTarget({ kind: 'one', entry })}
        />
      )}

      {entries.length > 0 && (
        <p className="text-xs text-muted-foreground text-center">
          {entries.length === 1
            ? t('trash.footerOne')
            : t('trash.footerMany', { count: entries.length })}
        </p>
      )}

        </div>
      </div>

      <PurgeDialog
        target={purgeTarget}
        onClose={() => setPurgeTarget(null)}
        onConfirm={onPurgeConfirm}
      />
    </div>
  )
}
