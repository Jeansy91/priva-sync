import { useCallback, useMemo, useState } from 'react'
import type { FileEntry, SyncRoot } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { useTranslation } from '@privasync/i18n'

// Hook owns the "create empty folder inside the current sync root" flow:
// dialog state, sibling-name lookup for duplicate validation, and the POST
// /directories call.  Lives outside FilesPage to keep that page below the
// 400-line page budget.
export function useCreateFolder(args: {
  selectedRoot: SyncRoot | null
  allFiles:     FileEntry[]
  currentPath:  string
  refresh:      () => Promise<void> | void
}) {
  const { t } = useTranslation()
  const { selectedRoot, allFiles, currentPath, refresh } = args

  const [open, setOpen]       = useState(false)
  const [parent, setParent]   = useState('/')
  const [loading, setLoading] = useState(false)
  const [error, setError]     = useState<string | null>(null)

  const openFor = useCallback((parentPath: string) => {
    setParent(parentPath || '/')
    setError(null)
    setOpen(true)
  }, [])

  const close = useCallback(() => setOpen(false), [])

  const existingNames = useMemo(() => {
    const prefix = parent.endsWith('/') ? parent : parent + '/'
    const names = new Set<string>()
    for (const f of allFiles) {
      if (!f.path.startsWith(prefix)) continue
      const rest = f.path.slice(prefix.length)
      if (!rest || rest.includes('/')) continue
      names.add(rest)
    }
    return Array.from(names)
  }, [allFiles, parent])

  const submit = useCallback(async (name: string) => {
    if (!selectedRoot) return
    const base = parent === '/' ? '' : parent
    setLoading(true)
    setError(null)
    try {
      await apiClient.files.createDirectory(selectedRoot.id, base + '/' + name)
      setOpen(false)
      await refresh()
    } catch (err: any) {
      setError(err?.body?.error ?? t('files.createFolderError'))
    } finally {
      setLoading(false)
    }
  }, [selectedRoot, parent, refresh, t])

  return {
    open,
    parent,
    loading,
    error,
    existingNames,
    openFor,
    close,
    submit,
    // Convenience for FilesPage: trigger create-folder in the path the user
    // currently has open in the breadcrumb.
    openHere: () => openFor(currentPath),
  }
}
