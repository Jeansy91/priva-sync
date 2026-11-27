import { useState } from 'react'
import { Select, getThemePreference, setThemePreference, type ThemePreference } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { SunMoon } from 'lucide-react'
import { SectionCard } from './SettingsPrimitives'

/**
 * Theme picker — light/dark/system. Stored per device in localStorage
 * (a theme is a device property, not a user-profile property).
 */
export function ThemeSection() {
  const { t } = useTranslation()
  const [pref, setPref] = useState<ThemePreference>(() => getThemePreference())

  const onChange = (next: ThemePreference) => {
    setThemePreference(next)
    setPref(next)
  }

  return (
    <SectionCard
      title={t('settings.theme')}
      description={t('settings.themeDescription')}
      icon={<SunMoon className="h-4 w-4 text-muted-foreground" />}
    >
      <div className="max-w-xs">
        <Select
          value={pref}
          onChange={(v) => onChange(v as ThemePreference)}
          options={[
            { value: 'system', label: t('settings.themeSystem') },
            { value: 'light',  label: t('settings.themeLight') },
            { value: 'dark',   label: t('settings.themeDark') },
          ]}
        />
      </div>
    </SectionCard>
  )
}
