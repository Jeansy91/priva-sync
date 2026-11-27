import { Languages } from 'lucide-react'
import { Select } from '@privasync/ui'
import {
  changeLanguage, useTranslation,
  SUPPORTED_LANGUAGES, LANGUAGE_AUTONYMS,
  type SupportedLanguage,
} from '@privasync/i18n'
import { STORED_LANG_KEY } from '../i18n/bootLanguage'

/**
 * Sprach-Dropdown für Pre-Login-Screens (Login, Setup,
 * Passwort-Reset). Persistiert die Wahl in localStorage; nach Login
 * überschreibt `user.preferred_language` aus `/me`.
 */
export default function PreLoginLanguagePicker() {
  const { i18n } = useTranslation()
  const current = (i18n.language?.split('-')[0] ?? 'en') as SupportedLanguage

  async function pick(lang: string) {
    const next = lang as SupportedLanguage
    if (next === current) return
    try { localStorage.setItem(STORED_LANG_KEY, next) } catch { /* private mode */ }
    await changeLanguage(next)
  }

  return (
    <div className="inline-flex items-center gap-2">
      <Languages className="h-4 w-4 text-muted-foreground" />
      <div className="w-32">
        <Select
          value={current}
          onChange={pick}
          options={SUPPORTED_LANGUAGES.map((lang) => ({
            value: lang,
            label: LANGUAGE_AUTONYMS[lang],
          }))}
        />
      </div>
    </div>
  )
}
