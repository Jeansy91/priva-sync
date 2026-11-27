import { useState } from 'react'
import { Switch } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { ListTree } from 'lucide-react'
import { apiClient, useAuthStore } from '../../store/auth'
import { Alert, SectionCard } from './SettingsPrimitives'

/**
 * Toggles the file-tree sidebar on FilesPage. Optimistic update — the
 * UI flips immediately and only rolls back if the API rejects.
 */
export function FileTreeSection() {
  const { t } = useTranslation()
  const { user, setUser } = useAuthStore()
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState<{ type: 'success' | 'error'; msg: string } | null>(null)

  const onToggle = async (next: boolean) => {
    if (!user) return
    setBusy(true)
    setStatus(null)
    // Optimistic update — the toggle takes effect immediately; the server
    // PUT is best-effort and rolled back in the store on failure.
    setUser({ ...user, show_file_tree: next })
    try {
      await apiClient.auth.setShowFileTree(next)
      setStatus({ type: 'success', msg: t('settings.saved') })
    } catch {
      setUser({ ...user, show_file_tree: !next })
      setStatus({ type: 'error', msg: t('settings.errorGeneric') })
    } finally {
      setBusy(false)
    }
  }

  return (
    <SectionCard
      title={t('settings.fileTreeTitle')}
      description={t('settings.fileTreeDescription')}
      icon={<ListTree className="h-4 w-4 text-muted-foreground" />}
    >
      <Switch
        checked={user?.show_file_tree ?? true}
        onChange={onToggle}
        disabled={busy}
        label={t('settings.fileTreeToggleLabel')}
      />
      {status && <div className="mt-3"><Alert type={status.type} message={status.msg} /></div>}
    </SectionCard>
  )
}
