import { useCallback, useMemo, useState } from 'react'
import { ApiRequestError, type FileEntry, type SyncRoot } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { useTranslation } from '@privasync/i18n'
import { parentPath } from './helpers'
import type { DirEntry } from './helpers'

// Hook owns the "copy a single entry" flow: dialog state, sibling-name lookup
// for duplicate validation, a sensible default copy name, and the PATCH
// op=copy call. Works for files and folders — the server resolves which from
// the source row and books the copied bytes against the user's quota.
export function useCopy(args: {
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
  const originalName = target?.name ?? ''

  const openFor = useCallback((entry: DirEntry) => {
    setError(null)
    setTarget(entry)
  }, [])

  const close = useCallback(() => setTarget(null), [])

  // Sibling names in the same directory — duplicate detection for the dialog.
  const existingNames = useMemo(() => {
    if (!fromPath) return []
    const parent = parentPath(fromPath)
    const prefix = parent === '/' ? '/' : parent + '/'
    const names = new Set<string>()
    for (const f of allFiles) {
      if (!f.path.startsWith(prefix)) continue
      const rest = f.path.slice(prefix.length)
      if (!rest || rest.includes('/')) continue
      names.add(rest)
    }
    return Array.from(names)
  }, [allFiles, fromPath])

  // Default name: "<base>-Kopie.<ext>" for files, "<name>-Kopie" for folders.
  // The suffix is localised so it reads naturally in the user's language.
  const defaultName = useMemo(() => {
    if (!target) return ''
    const suffix = t('files.copyNameSuffix')
    if (target.kind === 'dir') return originalName + suffix
    const dot = originalName.lastIndexOf('.')
    if (dot > 0) return originalName.slice(0, dot) + suffix + originalName.slice(dot)
    return originalName + suffix
  }, [target, originalName, t])

  const submit = useCallback(async (newName: string) => {
    if (!selectedRoot || !fromPath) return
    const parent = parentPath(fromPath)
    const base = parent === '/' ? '' : parent
    const toPath = base + '/' + newName
    setLoading(true)
    setError(null)
    try {
      await apiClient.files.copy(selectedRoot.id, fromPath, toPath)
      setTarget(null)
      await refresh()
    } catch (err: unknown) {
      if (err instanceof ApiRequestError && err.status === 507) {
        setError(t('files.copyQuotaExceeded'))
      } else {
        const body = (err as { body?: { error?: string } })?.body
        setError(body?.error ?? t('files.copyError'))
      }
    } finally {
      setLoading(false)
    }
  }, [selectedRoot, fromPath, refresh, t])

  return { open, target, originalName, defaultName, loading, error, existingNames, openFor, close, submit }
}
