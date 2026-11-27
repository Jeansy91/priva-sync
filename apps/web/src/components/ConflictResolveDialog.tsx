import { useState, useEffect } from 'react'
import { AlertCircle, FileClock, FileDiff, FileText, Loader2 } from 'lucide-react'
import {
  Button,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
  DiffView,
  ConflictStrategyChooser,
  ConflictVersionCompare,
  isPreviewableImage,
  PREVIEW_SIZE_LIMIT,
} from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import type { ConflictVersions, ResolveStrategy } from '@privasync/api-client'
import { apiClient } from '../store/auth'
import { diffLines, isProbablyText, type DiffLine } from '../lib/lineDiff'

export interface ConflictResolveTarget {
  id: string
  // Own-root conflicts: the sync root, needed to download the current
  // version for the text diff.
  rootId?: string
  // Set for conflicts inside a Shared Mount (ADR-0011): resolve and version
  // lookups must go through the mount-scoped endpoints.
  mountId?: string
}

// The diff loads both files into memory; cap each side to keep the dialog
// responsive (ADR-0003 names text files as the diff target).
const DIFF_SIZE_LIMIT = 1024 * 1024

type DiffState =
  | { kind: 'hidden' }
  | { kind: 'loading' }
  | { kind: 'ready'; lines: DiffLine[] }
  | { kind: 'binary' }
  | { kind: 'too-large' }
  | { kind: 'failed' }

/**
 * Resolve dialog (ADR-0003): shows both competing versions side by side and
 * lets the user choose a strategy — keep the current file, take the
 * conflicting version, or keep both. Nothing is discarded until they decide.
 */
export default function ConflictResolveDialog({
  conflict,
  localeTag,
  onClose,
  onResolved,
}: {
  conflict:   ConflictResolveTarget | null
  localeTag:  string
  onClose:    () => void
  onResolved: (conflictID: string) => void
}) {
  const { t } = useTranslation()
  const [versions, setVersions] = useState<ConflictVersions | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [resolving, setResolving] = useState<ResolveStrategy | null>(null)
  const [diff, setDiff] = useState<DiffState>({ kind: 'hidden' })
  const [previews, setPreviews] = useState<{ current: string; incoming: string } | null>(null)

  // Both versions' bytes: the live file and the loser copy.
  const fetchBoth = (c: ConflictResolveTarget, v: ConflictVersions) =>
    Promise.all([
      c.mountId
        ? apiClient.sharedMounts.download(c.mountId, v.file_path)
        : apiClient.files.download(c.rootId ?? '', v.file_path),
      c.mountId
        ? apiClient.sharedMounts.conflictFile(c.mountId, c.id)
        : apiClient.files.conflictFile(c.id),
    ])

  // Images are compared visually: load both and show them next to each other.
  useEffect(() => {
    setPreviews(null)
    if (!conflict || !versions) return
    if (!isPreviewableImage(versions.file_path)) return
    if (versions.current.size > PREVIEW_SIZE_LIMIT || versions.conflict.size > PREVIEW_SIZE_LIMIT) return
    let cancelled = false
    const urls: string[] = []
    fetchBoth(conflict, versions)
      .then(async ([currentRes, loserRes]) => {
        if (!currentRes.ok || !loserRes.ok) return
        const [currentBlob, loserBlob] = await Promise.all([currentRes.blob(), loserRes.blob()])
        if (cancelled) return
        urls.push(URL.createObjectURL(currentBlob), URL.createObjectURL(loserBlob))
        setPreviews({ current: urls[0], incoming: urls[1] })
      })
      .catch(() => { /* the facts above still allow a decision */ })
    return () => {
      cancelled = true
      urls.forEach((u) => URL.revokeObjectURL(u))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conflict, versions])

  useEffect(() => {
    setVersions(null)
    setError(null)
    setResolving(null)
    setDiff({ kind: 'hidden' })
    if (!conflict) return
    let cancelled = false
    const load = conflict.mountId
      ? apiClient.sharedMounts.conflictVersions(conflict.mountId, conflict.id)
      : apiClient.files.getConflict(conflict.id)
    load
      .then((v) => { if (!cancelled) setVersions(v) })
      .catch(() => { if (!cancelled) setError(t('conflicts.resolveDialog.loadFailed')) })
    return () => { cancelled = true }
  }, [conflict, t])

  const handleShowDiff = async () => {
    if (!conflict || !versions) return
    if (versions.current.size > DIFF_SIZE_LIMIT || versions.conflict.size > DIFF_SIZE_LIMIT) {
      setDiff({ kind: 'too-large' })
      return
    }
    setDiff({ kind: 'loading' })
    try {
      const [currentRes, loserRes] = await fetchBoth(conflict, versions)
      if (!currentRes.ok || !loserRes.ok) {
        setDiff({ kind: 'failed' })
        return
      }
      const [currentText, loserText] = await Promise.all([currentRes.text(), loserRes.text()])
      if (!isProbablyText(currentText) || !isProbablyText(loserText)) {
        setDiff({ kind: 'binary' })
        return
      }
      setDiff({ kind: 'ready', lines: diffLines(currentText, loserText) })
    } catch {
      setDiff({ kind: 'failed' })
    }
  }

  const handleResolve = async (strategy: ResolveStrategy) => {
    if (!conflict) return
    setResolving(strategy)
    setError(null)
    try {
      if (conflict.mountId) {
        await apiClient.sharedMounts.resolveConflict(conflict.mountId, conflict.id, strategy)
      } else {
        await apiClient.files.resolveConflict(conflict.id, strategy)
      }
      onResolved(conflict.id)
      onClose()
    } catch {
      setError(t('conflicts.resolveFailed'))
    } finally {
      setResolving(null)
    }
  }

  return (
    <Dialog open={conflict !== null} onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="max-w-2xl">
        <DialogClose onClose={onClose} />
        <DialogHeader>
          <DialogTitle>{t('conflicts.resolveDialog.title')}</DialogTitle>
          <DialogDescription>{t('conflicts.resolveDialog.description')}</DialogDescription>
        </DialogHeader>

        {error && (
          <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive mb-4">
            <AlertCircle className="h-4 w-4 shrink-0" />
            {error}
          </div>
        )}

        {!versions && !error ? (
          <div className="flex items-center justify-center py-10 gap-2 text-muted-foreground">
            <Loader2 className="h-5 w-5 animate-spin" />
            <span className="text-sm">{t('conflicts.loading')}</span>
          </div>
        ) : versions && (
          <>
            <ConflictVersionCompare
              locale={localeTag}
              sizeLabel={t('conflicts.resolveDialog.size')}
              unknownLabel={t('conflicts.resolveDialog.unknown')}
              identicalLabel={t('conflicts.resolveDialog.identical')}
              current={{
                label: t('conflicts.resolveDialog.current'),
                path: versions.file_path,
                icon: <FileText className="h-4 w-4 text-muted-foreground" />,
                sizeBytes: versions.current.size,
                modifiedAt: versions.current.mtime,
                modifiedLabel: t('conflicts.resolveDialog.modified'),
                previewSrc: previews?.current,
              }}
              incoming={{
                label: t('conflicts.resolveDialog.incoming'),
                path: versions.conflict_path,
                icon: <FileClock className="h-4 w-4 text-amber-500" />,
                sizeBytes: versions.conflict.size,
                // Older conflicts carry no mtime of the loser copy; fall back to
                // when the conflict was detected so the row is never empty.
                modifiedAt: versions.conflict.mtime ?? versions.conflict.created_at,
                modifiedComparable: versions.conflict.mtime !== null,
                modifiedLabel: versions.conflict.mtime
                  ? t('conflicts.resolveDialog.modified')
                  : t('conflicts.resolveDialog.detected'),
                previewSrc: previews?.incoming,
              }}
            />

            <div className="mt-3">
              <Button
                variant="outline"
                size="sm"
                onClick={() => (diff.kind === 'hidden' ? handleShowDiff() : setDiff({ kind: 'hidden' }))}
                disabled={diff.kind === 'loading'}
              >
                {diff.kind === 'loading'
                  ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  : <FileDiff className="h-3.5 w-3.5" />}
                {diff.kind === 'hidden' || diff.kind === 'loading'
                  ? t('conflicts.resolveDialog.showDiff')
                  : t('conflicts.resolveDialog.hideDiff')}
              </Button>

              {diff.kind === 'binary' && (
                <p className="mt-2 text-xs text-muted-foreground">{t('conflicts.resolveDialog.diffBinary')}</p>
              )}
              {diff.kind === 'too-large' && (
                <p className="mt-2 text-xs text-muted-foreground">{t('conflicts.resolveDialog.diffTooLarge')}</p>
              )}
              {diff.kind === 'failed' && (
                <p className="mt-2 text-xs text-destructive">{t('conflicts.resolveDialog.diffFailed')}</p>
              )}
              {diff.kind === 'ready' && (
                <DiffView
                  lines={diff.lines}
                  currentLabel={t('conflicts.resolveDialog.diffCurrentLabel')}
                  incomingLabel={t('conflicts.resolveDialog.diffIncomingLabel')}
                />
              )}
            </div>

            <div className="mt-4">
              <ConflictStrategyChooser
                disabled={resolving !== null}
                onSelect={(key) => handleResolve(key as ResolveStrategy)}
                options={[
                  {
                    key:   'keep_current',
                    label: t('conflicts.resolveDialog.keepCurrent'),
                    hint:  t('conflicts.resolveDialog.keepCurrentHint'),
                    busy:  resolving === 'keep_current',
                  },
                  {
                    key:   'keep_conflict',
                    label: t('conflicts.resolveDialog.keepConflict'),
                    hint:  t('conflicts.resolveDialog.keepConflictHint'),
                    busy:  resolving === 'keep_conflict',
                  },
                  {
                    key:   'keep_both',
                    label: t('conflicts.resolveDialog.keepBoth'),
                    hint:  t('conflicts.resolveDialog.keepBothHint'),
                    busy:  resolving === 'keep_both',
                  },
                ]}
              />
            </div>
          </>
        )}

        <DialogFooter>
          <Button variant="outline" size="sm" onClick={onClose} disabled={resolving !== null}>
            {t('conflicts.resolveDialog.cancel')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
