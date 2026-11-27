import { useTranslation } from '@privasync/i18n'
import { IdCard, Lock, Palette } from 'lucide-react'
import { useScrolled } from '../hooks/useScrolled'
import { SettingsGroup } from './settings/SettingsPrimitives'
import { ProfileSection } from './settings/ProfileSection'
import { AvatarSection } from './settings/AvatarSection'
import { LanguageSection } from './settings/LanguageSection'
import { TOTPSection } from './settings/TOTPSection'
import { PasskeySection } from './settings/PasskeySection'
import { PasswordSection } from './settings/PasswordSection'
import { AppPasswordSection } from './settings/AppPasswordSection'
import { ThemeSection } from './settings/ThemeSection'
import { FileTreeSection } from './settings/FileTreeSection'
import { FileCheckboxesSection } from './settings/FileCheckboxesSection'
import { PageHeader } from '../components/PageHeader'

export default function SettingsPage() {
  const { t } = useTranslation()
  const { scrolled, onScroll } = useScrolled()
  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title={t('settings.title')}
        subtitle={t('settings.subtitle')}
        scrolled={scrolled}
      />
      <div className="flex-1 overflow-auto" onScroll={onScroll}>
        <div className="max-w-7xl mx-auto p-4 sm:p-6 space-y-10">
          <SettingsGroup
            title={t('settings.groups.account')}
            description={t('settings.groups.accountDescription')}
            icon={<IdCard className="h-5 w-5" />}
          >
            <ProfileSection />
            <AvatarSection />
            <LanguageSection />
          </SettingsGroup>
          <SettingsGroup
            title={t('settings.groups.security')}
            description={t('settings.groups.securityDescription')}
            icon={<Lock className="h-5 w-5" />}
          >
            <TOTPSection />
            <PasskeySection />
            <PasswordSection />
            <AppPasswordSection />
          </SettingsGroup>
          <SettingsGroup
            title={t('settings.groups.appearance')}
            description={t('settings.groups.appearanceDescription')}
            icon={<Palette className="h-5 w-5" />}
          >
            <ThemeSection />
            <FileTreeSection />
            <FileCheckboxesSection />
          </SettingsGroup>
        </div>
      </div>
    </div>
  )
}
