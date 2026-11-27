import { useState } from 'react'
import { Switch } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { CheckSquare } from 'lucide-react'
import { apiClient, useAuthStore } from '../../store/auth'
import { Alert, SectionCard } from './SettingsPrimitives'

/**
 * Toggles the multiselect checkbox overlay on FilesPage table/grid
 * rows. Same optimistic-update pattern as FileTreeSection.
 */
export function FileCheckboxesSection() {
  const { t } = useTranslation()
  const { user, setUser } = useAuthStore()
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState<{ type: 'success' | 'error'; msg: string } | null>(null)

  const onToggle = async (next: boolean) => {
    if (!user) return
    setBusy(true)
    setStatus(null)
    setUser({ ...user, show_file_checkboxes: next })
    try {
      await apiClient.auth.setShowFileCheckboxes(next)
      setStatus({ type: 'success', msg: t('settings.saved') })
    } catch {
      setUser({ ...user, show_file_checkboxes: !next })
      setStatus({ type: 'error', msg: t('settings.errorGeneric') })
    } finally {
      setBusy(false)
    }
  }

  return (
    <SectionCard
      title={t('settings.fileCheckboxesTitle')}
      description={t('settings.fileCheckboxesDescription')}
      icon={<CheckSquare className="h-4 w-4 text-muted-foreground" />}
    >
      <Switch
        checked={user?.show_file_checkboxes ?? true}
        onChange={onToggle}
        disabled={busy}
        label={t('settings.fileCheckboxesToggleLabel')}
      />
      {status && <div className="mt-3"><Alert type={status.type} message={status.msg} /></div>}
    </SectionCard>
  )
}
