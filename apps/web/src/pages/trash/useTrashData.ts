import { useState, useEffect, useCallback } from 'react'
import { ApiRequestError, typed, type SyncRoot } from '@privasync/api-client'
import { apiClient, useAuthStore } from '../../store/auth'
import { useSSE } from '../../hooks/useSSE'
import { fileName, type TrashEntryWithRoot } from './trashHelpers'

interface UseTrashDataResult {
  syncRoots:    SyncRoot[]
  entries:      TrashEntryWithRoot[]
  loading:      boolean
  error:        string | null
  restoreError: string | null
  setRestoreError: (msg: string | null) => void
  restoring:    string | null
  purging:      string | null
  purgingAll:   boolean
  bulkBusy:     boolean
  handleRestore: (entry: TrashEntryWithRoot) => Promise<void>
  doPurgeOne:    (entry: TrashEntryWithRoot) => Promise<void>
  doPurgeAll:    () => Promise<void>
  doRestoreEntries: (targets: TrashEntryWithRoot[]) => Promise<void>
  doPurgeEntries:   (targets: TrashEntryWithRoot[]) => Promise<void>
  // Pass-throughs the page wires into the dialog/SSE.
  setEntries: React.Dispatch<React.SetStateAction<TrashEntryWithRoot[]>>
}

/**
 * Owns the trash data layer for TrashPage: inventory loading, the four
 * mutation flows (single restore/purge, purge-all, bulk
 * restore/purge), live SSE updates, and the user-quota refresh that
 * keeps the sidebar bar in sync after each mutation.
 */
export function useTrashData(
  t: (key: string, vars?: Record<string, unknown>) => string,
  setSelectedIds: React.Dispatch<React.SetStateAction<Set<string>>>,
): UseTrashDataResult {
  const { setUser } = useAuthStore()
  const [syncRoots, setSyncRoots] = useState<SyncRoot[]>([])
  const [entries, setEntries] = useState<TrashEntryWithRoot[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [restoring, setRestoring] = useState<string | null>(null)
  const [restoreError, setRestoreError] = useState<string | null>(null)
  const [purging, setPurging] = useState<string | null>(null)
  const [purgingAll, setPurgingAll] = useState(false)
  const [bulkBusy, setBulkBusy] = useState(false)

  const refreshQuota = async () => {
    // Re-fetch user so the sidebar quota bar + trash byte counter reflect the
    // freed bytes immediately, without waiting for a navigation.
    try { setUser(await apiClient.auth.me()) } catch { /* non-critical */ }
  }

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    setRestoreError(null)
    try {
      const roots = await apiClient.files.listSyncRoots()
      setSyncRoots(roots ?? [])

      const all = await Promise.all(
        (roots ?? []).map(async (root) => {
          const items = await apiClient.trash.list(root.id)
          return (items ?? []).map((e): TrashEntryWithRoot => ({
            ...e,
            rootID:   root.id,
            rootName: root.name,
          }))
        })
      )

      setEntries(
        all.flat().sort((a, b) => {
          if (!a.deleted_at) return 1
          if (!b.deleted_at) return -1
          return new Date(b.deleted_at).getTime() - new Date(a.deleted_at).getTime()
        })
      )
    } catch {
      setError(t('trash.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { load() }, [load])

  useSSE({
    trash_added: typed<'trash_added'>(({ sync_root_id, file_id, path, size }) => {
      const root = syncRoots.find((r) => r.id === sync_root_id)
      const entry: TrashEntryWithRoot = {
        id: file_id,
        path,
        size,
        content_hash: '',
        deleted_at: new Date().toISOString(),
        rootID:   sync_root_id,
        rootName: root?.name ?? '',
      }
      setEntries((prev) => (prev.some((e) => e.id === entry.id) ? prev : [entry, ...prev]))
      refreshQuota()
    }),
    file_restored: typed<'file_restored'>(({ file_id }) => {
      setEntries((prev) => prev.filter((e) => e.id !== file_id))
      refreshQuota()
    }),
    trash_emptied: typed<'trash_emptied'>(() => {
      setEntries([])
      refreshQuota()
    }),
    trash_purged: (data: unknown) => {
      const m = data as { file_id?: string }
      if (!m.file_id) return
      setEntries((prev) => prev.filter((e) => e.id !== m.file_id))
      refreshQuota()
    },
    quota_updated: typed<'quota_updated'>(() => { refreshQuota() }),
  }, load)

  const handleRestore = async (entry: TrashEntryWithRoot) => {
    setRestoring(entry.id)
    setRestoreError(null)
    try {
      await apiClient.trash.restore(entry.rootID, entry.id)
      setEntries((prev) => prev.filter((e) => e.id !== entry.id))
      await refreshQuota()
    } catch (err: unknown) {
      // 410 Gone = server has already purged the (broken) row; the list
      // must still drop the entry.
      if (err instanceof ApiRequestError && err.status === 410) {
        setEntries((prev) => prev.filter((e) => e.id !== entry.id))
        return
      }
      const msg = (err as { body?: { error?: string } } | null)?.body?.error ?? t('trash.restoreFailed')
      setRestoreError(`${fileName(entry.path)}: ${msg}`)
    } finally {
      setRestoring(null)
    }
  }

  const doPurgeOne = async (entry: TrashEntryWithRoot) => {
    setPurging(entry.id)
    setRestoreError(null)
    try {
      await apiClient.trash.purge(entry.rootID, entry.id)
      setEntries((prev) => prev.filter((e) => e.id !== entry.id))
      await refreshQuota()
    } catch (err: unknown) {
      const msg = (err as { body?: { error?: string } } | null)?.body?.error ?? t('trash.purgeFailed')
      setRestoreError(`${fileName(entry.path)}: ${msg}`)
    } finally {
      setPurging(null)
    }
  }

  const doPurgeAll = async () => {
    if (entries.length === 0) return
    setPurgingAll(true)
    setRestoreError(null)
    try {
      await apiClient.trash.empty()
      setEntries([])
    } catch {
      setRestoreError(t('trash.purgeFailed'))
    }
    await refreshQuota()
    setPurgingAll(false)
  }

  const doRestoreEntries = async (targets: TrashEntryWithRoot[]) => {
    if (targets.length === 0) return
    setBulkBusy(true)
    setRestoreError(null)
    const failed: TrashEntryWithRoot[] = []
    for (const entry of targets) {
      try {
        await apiClient.trash.restore(entry.rootID, entry.id)
      } catch (err: unknown) {
        if (err instanceof ApiRequestError && err.status === 410) continue
        failed.push(entry)
      }
    }
    const failedIds = new Set(failed.map((e) => e.id))
    const targetIds = new Set(targets.map((e) => e.id))
    setEntries((prev) => prev.filter((e) => failedIds.has(e.id) || !targetIds.has(e.id)))
    setSelectedIds((prev) => {
      const next = new Set(prev)
      for (const id of targetIds) if (!failedIds.has(id)) next.delete(id)
      return next
    })
    if (failed.length > 0) setRestoreError(t('trash.restoreFailed'))
    await refreshQuota()
    setBulkBusy(false)
  }

  const doPurgeEntries = async (targets: TrashEntryWithRoot[]) => {
    if (targets.length === 0) return
    setBulkBusy(true)
    setRestoreError(null)
    const remaining: TrashEntryWithRoot[] = []
    for (const entry of targets) {
      try {
        await apiClient.trash.purge(entry.rootID, entry.id)
      } catch {
        remaining.push(entry)
      }
    }
    const remainingIds = new Set(remaining.map((e) => e.id))
    const targetIds = new Set(targets.map((e) => e.id))
    setEntries((prev) => prev.filter((e) => remainingIds.has(e.id) || !targetIds.has(e.id)))
    setSelectedIds((prev) => {
      const next = new Set(prev)
      for (const id of targetIds) if (!remainingIds.has(id)) next.delete(id)
      return next
    })
    if (remaining.length > 0) {
      setRestoreError(
        remaining.length === 1
          ? t('trash.purgeAllPartialOne')
          : t('trash.purgeAllPartialMany', { count: remaining.length }),
      )
    }
    await refreshQuota()
    setBulkBusy(false)
  }

  return {
    syncRoots, entries, loading, error, restoreError, setRestoreError,
    restoring, purging, purgingAll, bulkBusy,
    handleRestore, doPurgeOne, doPurgeAll,
    doRestoreEntries, doPurgeEntries,
    setEntries,
  }
}
