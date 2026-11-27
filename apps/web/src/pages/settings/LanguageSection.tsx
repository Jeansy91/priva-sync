import { useState } from 'react'
import { Select } from '@privasync/ui'
import { changeLanguage, useTranslation, LANGUAGE_AUTONYMS, type SupportedLanguage } from '@privasync/i18n'
import { Languages } from 'lucide-react'
import { apiClient, useAuthStore } from '../../store/auth'
import { Alert, SectionCard } from './SettingsPrimitives'

/**
 * Language picker — persists the choice to the server (so mail templates
 * pick it up) and applies it locally via i18n.changeLanguage so the UI
 * switches without reload.
 */
export function LanguageSection() {
  const { t, i18n } = useTranslation()
  const { user, setUser } = useAuthStore()
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState<{ type: 'success' | 'error'; msg: string } | null>(null)

  const onChange = async (next: SupportedLanguage) => {
    if (next === user?.preferred_language) return
    setBusy(true)
    setStatus(null)
    try {
      await apiClient.auth.setLanguage(next)
      await changeLanguage(next)
      const me = await apiClient.auth.me()
      setUser(me)
      setStatus({ type: 'success', msg: t('settings.saved') })
    } catch {
      setStatus({ type: 'error', msg: t('settings.errorGeneric') })
    } finally {
      setBusy(false)
    }
  }

  // '' = not set yet -> show the currently active (browser/OS-detected) language.
  const current = user?.preferred_language || (i18n.language?.split('-')[0] as SupportedLanguage) || 'en'
  return (
    <SectionCard
      title={t('settings.language')}
      description={t('settings.languageDescription')}
      icon={<Languages className="h-4 w-4 text-muted-foreground" />}
    >
      <div className="max-w-xs">
        <Select
          value={current}
          onChange={(v) => onChange(v as SupportedLanguage)}
          disabled={busy}
          options={[
            { value: 'de', label: LANGUAGE_AUTONYMS.de },
            { value: 'en', label: LANGUAGE_AUTONYMS.en },
          ]}
        />
      </div>
      {status && <div className="mt-3"><Alert type={status.type} message={status.msg} /></div>}
    </SectionCard>
  )
}
