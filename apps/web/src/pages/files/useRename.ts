import { useCallback, useMemo, useState } from 'react'
import type { FileEntry, SyncRoot } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { useTranslation } from '@privasync/i18n'
import { parentPath } from './helpers'
import type { DirEntry } from './helpers'

// Hook owns the "rename a single entry" flow: dialog state, sibling-name
// lookup for duplicate validation, and the PATCH op=move call (rename keeps
// the parent and only swaps the last segment). Works for files and folders —
// the server rewrites the whole subtree for a directory. Sync-root rename is
// a separate action (it targets the root's name, not a path).
export function useRename(args: {
  selectedRoot: SyncRoot | null
  allFiles:     FileEntry[]
  refresh:      () => Promise<void> | void
}) {
  const { t } = useTranslation()
  const { selectedRoot, allFiles, refresh } = args

  const [target, setTarget]   = useState<DirEntry | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError]     = useState<string | null>(null)

  const open = target !== null
  const fromPath = target === null ? '' : target.kind === 'file' ? target.file.path : target.path

  const openFor = useCallback((entry: DirEntry) => {
    setError(null)
    setTarget(entry)
  }, [])

  const close = useCallback(() => setTarget(null), [])

  // Sibling names in the same directory, minus the entry being renamed —
  // renaming to its own current name is caught separately as "unchanged".
  const existingNames = useMemo(() => {
    if (!fromPath) return []
    const parent = parentPath(fromPath)
    const prefix = parent === '/' ? '/' : parent + '/'
    const own = fromPath.slice(prefix.length)
    const names = new Set<string>()
    for (const f of allFiles) {
      if (!f.path.startsWith(prefix)) continue
      const rest = f.path.slice(prefix.length)
      if (!rest || rest.includes('/') || rest === own) continue
      names.add(rest)
    }
    return Array.from(names)
  }, [allFiles, fromPath])

  const submit = useCallback(async (newName: string) => {
    if (!selectedRoot || !fromPath) return
    const parent = parentPath(fromPath)
    const base = parent === '/' ? '' : parent
    const toPath = base + '/' + newName
    setLoading(true)
    setError(null)
    try {
      await apiClient.files.rename(selectedRoot.id, fromPath, toPath)
      setTarget(null)
      await refresh()
    } catch (err: any) {
      setError(err?.body?.error ?? t('files.renameError'))
    } finally {
      setLoading(false)
    }
  }, [selectedRoot, fromPath, refresh, t])

  return { open, target, loading, error, existingNames, openFor, close, submit }
}
