import { useState, useEffect, useCallback } from 'react'
import { AlertTriangle, CheckCircle, Loader2, AlertCircle, Download } from 'lucide-react'
import { Button, cn } from '@privasync/ui'
import { apiClient } from '../store/auth'
import { useSSE } from '../hooks/useSSE'
import { typed, type SyncRoot, type Conflict } from '@privasync/api-client'
import { useTranslation } from '@privasync/i18n'
import { useScrolled } from '../hooks/useScrolled'
import { TitleWrap } from '../components/TitleWrap'
import ConflictResolveDialog, { type ConflictResolveTarget } from '../components/ConflictResolveDialog'
import { PageHeader } from '../components/PageHeader'

interface ConflictWithRoot extends Conflict {
  rootName: string
  rootId:   string
  // For conflicts inside a Shared Mount (ADR-0011): the mount's share id and
  // whether the recipient may resolve it. Undefined for own-root conflicts.
  mountId?:    string
  canResolve:  boolean
}

function formatDate(iso: string, locale: string): string {
  return new Date(iso).toLocaleString(locale, {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit',
  })
}

export default function ConflictsPage() {
  const { t, i18n } = useTranslation()
  const localeTag = i18n.language === 'de' ? 'de-DE' : 'en-US'
  const { scrolled, onScroll } = useScrolled()
  const [syncRoots, setSyncRoots] = useState<SyncRoot[]>([])
  const [conflicts, setConflicts] = useState<ConflictWithRoot[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [resolveTarget, setResolveTarget] = useState<ConflictResolveTarget | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const roots = await apiClient.files.listSyncRoots()
      setSyncRoots(roots)

      // Own-root conflicts: the caller owns the root, so always resolvable.
      const ownConflicts = await Promise.all(
        roots.map(async (root) => {
          const cs = await apiClient.files.listConflicts(root.id)
          return (cs ?? []).map((c): ConflictWithRoot => ({
            ...c, rootName: root.name, rootId: root.id, canResolve: true,
          }))
        })
      )

      // Mount conflicts (ADR-0011): a conflict inside an incoming Shared Mount.
      // The conflict rows come back relativized + scoped to share.path; only
      // resolvable when the mount is writable.
      const mounts = await apiClient.sharedMounts.list()
      const mountConflicts = await Promise.all(
        mounts.map(async (m): Promise<ConflictWithRoot[]> => {
          const cs = await apiClient.sharedMounts.conflicts(m.id)
          return (cs ?? []).map((c): ConflictWithRoot => ({
            id:          c.id,
            file_id:     c.file_id,
            conflict_path: c.conflict_path,
            client_id:   null,
            resolved:    false,
            created_at:  c.created_at,
            resolved_at: null,
            rootName:    `${m.name} (${m.owner.username})`,
            rootId:      m.id,
            mountId:     m.id,
            canResolve:  m.can_write,
          }))
        })
      )

      setConflicts([...ownConflicts.flat(), ...mountConflicts.flat()].sort((a, b) =>
        new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
      ))
    } catch {
      setError(t('conflicts.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { load() }, [load])

  // Push: andere Sessions oder der Sync-Client koennen Konflikte erzeugen
  // bzw. aufloesen. Optimistic Patch statt vollem Reload, damit die Liste
  // ohne sichtbares Flackern aktualisiert.
  useSSE({
    conflict_added: typed<'conflict_added'>(({ conflict_id, file_id, sync_root_id, conflict_path, created_at }) => {
      const root = syncRoots.find((r) => r.id === sync_root_id)
      const entry: ConflictWithRoot = {
        id:            conflict_id,
        file_id,
        conflict_path,
        client_id:     null,
        resolved:      false,
        created_at,
        resolved_at:   null,
        rootName:      root?.name ?? '',
        rootId:        sync_root_id,
        canResolve:    true,
      }
      setConflicts((prev) =>
        prev.some((c) => c.id === entry.id)
          ? prev
          : [entry, ...prev],
      )
    }),
    conflict_resolved: (data: unknown) => {
      const m = data as { conflict_id?: string }
      if (!m.conflict_id) return
      setConflicts((prev) =>
        prev.map((c) =>
          c.id === m.conflict_id
            ? { ...c, resolved: true, resolved_at: new Date().toISOString() }
            : c,
        ),
      )
    },
  }, load)

  const handleDownload = async (conflict: ConflictWithRoot) => {
    try {
      // Conflict-scoped endpoint: upload/move loser copies have no files row,
      // so the path-based download cannot serve them.
      const res = conflict.mountId
        ? await apiClient.sharedMounts.conflictFile(conflict.mountId, conflict.id)
        : await apiClient.files.conflictFile(conflict.id)
      if (!res.ok) {
        setError(t('conflicts.downloadFailed'))
        return
      }
      const blob = await res.blob()
      const url  = URL.createObjectURL(blob)
      const a    = document.createElement('a')
      a.href     = url
      a.download = conflict.conflict_path.split('/').pop() || 'conflict'
      a.click()
      URL.revokeObjectURL(url)
    } catch {
      setError(t('conflicts.downloadFailed'))
    }
  }

  // Opens the strategy dialog; the dialog performs the actual resolve call.
  const handleResolve = (conflictID: string) => {
    const target = conflicts.find((c) => c.id === conflictID)
    if (!target) return
    setResolveTarget({ id: target.id, rootId: target.rootId, mountId: target.mountId })
  }

  const markResolved = (conflictID: string) => {
    setConflicts((prev) =>
      prev.map((c) =>
        c.id === conflictID
          ? { ...c, resolved: true, resolved_at: new Date().toISOString() }
          : c
      )
    )
  }

  const open = conflicts.filter((c) => !c.resolved)
  const resolved = conflicts.filter((c) => c.resolved)

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title={t('conflicts.title')}
        subtitle={t('conflicts.subtitle')}
        scrolled={scrolled}
      />

      <div className="flex-1 overflow-auto" onScroll={onScroll}>
        <div className="max-w-7xl mx-auto p-4 sm:p-6 space-y-6">
          {error && (
            <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              <AlertCircle className="h-4 w-4 shrink-0" />
              {error}
            </div>
          )}

          {loading && conflicts.length === 0 ? (
            <div className="flex items-center justify-center py-20 gap-2 text-muted-foreground">
              <Loader2 className="h-5 w-5 animate-spin" />
              <span className="text-sm">{t('conflicts.loading')}</span>
            </div>
          ) : open.length === 0 && !loading ? (
            <div className="flex flex-col items-center justify-center py-20 gap-3 text-center">
              <CheckCircle className="h-12 w-12 text-green-500" />
              <div>
                <p className="text-base font-medium">{t('conflicts.noneTitle')}</p>
                <p className="text-sm text-muted-foreground mt-1">
                  {t('conflicts.noneHint')}
                </p>
              </div>
            </div>
          ) : (
            <section className="space-y-3">
              <h2 className="text-sm font-medium text-muted-foreground uppercase tracking-wide">
                {t('conflicts.openSection', { count: open.length })}
              </h2>
              {open.map((conflict) => (
                <ConflictCard
                  key={conflict.id}
                  conflict={conflict}
                  onResolve={conflict.canResolve ? handleResolve : undefined}
                  onDownload={handleDownload}
                  localeTag={localeTag}
                />
              ))}
            </section>
          )}

          {resolved.length > 0 && (
            <section className="space-y-3">
              <h2 className="text-sm font-medium text-muted-foreground uppercase tracking-wide">
                {t('conflicts.resolvedSection', { count: resolved.length })}
              </h2>
              {resolved.map((conflict) => (
                <ConflictCard key={conflict.id} conflict={conflict} localeTag={localeTag} />
              ))}
            </section>
          )}
        </div>
      </div>

      <ConflictResolveDialog
        conflict={resolveTarget}
        localeTag={localeTag}
        onClose={() => setResolveTarget(null)}
        onResolved={markResolved}
      />
    </div>
  )
}

function ConflictCard({
  conflict,
  onResolve,
  onDownload,
  localeTag,
}: {
  conflict:   ConflictWithRoot
  onResolve?: (id: string) => void
  onDownload?: (conflict: ConflictWithRoot) => void
  localeTag:  string
}) {
  const { t } = useTranslation()
  return (
    <div
      className={cn(
        'rounded-lg border p-4 space-y-3',
        conflict.resolved
          ? 'border-border bg-muted/30 opacity-60'
          : 'border-amber-500/40 bg-amber-50/30 dark:bg-amber-950/20'
      )}
    >
      <div className="flex items-start gap-3">
        <AlertTriangle
          className={cn(
            'h-4 w-4 mt-0.5 shrink-0',
            conflict.resolved ? 'text-muted-foreground' : 'text-amber-500'
          )}
        />
        <div className="flex-1 min-w-0 space-y-1">
          <p className="text-sm font-medium truncate">
            {conflict.conflict_path}
          </p>
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
            <span>{t('conflicts.syncRoot')}: <strong>{conflict.rootName}</strong></span>
            <span>{t('conflicts.detected')}: {formatDate(conflict.created_at, localeTag)}</span>
            {conflict.resolved && conflict.resolved_at && (
              <span>{t('conflicts.resolved')}: {formatDate(conflict.resolved_at, localeTag)}</span>
            )}
          </div>
        </div>

        {!conflict.resolved && (
          <div className="flex items-center gap-2 shrink-0">
            {onDownload && (
              <TitleWrap title={t('conflicts.download')}>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => onDownload(conflict)}
                  aria-label={t('conflicts.download')}
                >
                  <Download className="h-3.5 w-3.5" />
                  {t('conflicts.download')}
                </Button>
              </TitleWrap>
            )}
            {onResolve && (
              <TitleWrap title={t('conflicts.resolve')}>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => onResolve(conflict.id)}
                  aria-label={t('conflicts.resolve')}
                >
                  <CheckCircle className="h-3.5 w-3.5" />
                  {t('conflicts.resolve')}
                </Button>
              </TitleWrap>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
