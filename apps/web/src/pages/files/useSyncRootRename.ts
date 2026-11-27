import { useCallback, useMemo, useState } from 'react'
import type { SyncRoot } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { useTranslation } from '@privasync/i18n'

// Hook owns the "rename a sync root" flow: dialog state, sibling-name lookup
// for duplicate validation, and the PATCH /sync-roots/{id} call. Distinct from
// useRename (which renames a path inside a root via op=move) — this targets the
// root's display name, leaves the storage path untouched, and reloads the root
// list on success. See ADR-0009.
export function useSyncRootRename(args: {
  syncRoots: SyncRoot[]
  reload:    () => Promise<void> | void
}) {
  const { t } = useTranslation()
  const { syncRoots, reload } = args

  const [target, setTarget]   = useState<SyncRoot | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError]     = useState<string | null>(null)

  const open = target !== null

  const openFor = useCallback((root: SyncRoot) => {
    setError(null)
    setTarget(root)
  }, [])

  const close = useCallback(() => setTarget(null), [])

  // Other roots' names (the rename target excluded) so the dialog can flag a
  // duplicate before the server 409s.
  const existingNames = useMemo(
    () => syncRoots.filter((r) => r.id !== target?.id).map((r) => r.name),
    [syncRoots, target],
  )

  const submit = useCallback(async (name: string) => {
    if (!target) return
    setLoading(true)
    setError(null)
    try {
      await apiClient.files.renameSyncRoot(target.id, name)
      setTarget(null)
      await reload()
    } catch (err: any) {
      setError(err?.body?.error ?? t('files.renameError'))
    } finally {
      setLoading(false)
    }
  }, [target, reload, t])

  return { open, target, loading, error, existingNames, openFor, close, submit }
}
