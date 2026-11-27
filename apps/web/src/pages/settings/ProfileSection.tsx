import { useState } from 'react'
import { useTranslation } from '@privasync/i18n'
import { User as UserIcon } from 'lucide-react'
import { apiClient, useAuthStore } from '../../store/auth'
import { ApiRequestError } from '@privasync/api-client'
import { EditableField } from './EditableField'
import { Alert, SectionCard } from './SettingsPrimitives'

function formatBytes(b: number): string {
  if (b === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(b) / Math.log(k))
  return `${parseFloat((b / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

/**
 * Account-identity section: username + email (both EditableField), role
 * pill, and the storage usage bar. Email change runs through the
 * request → confirm flow; the immediate save only kicks off the
 * confirmation mail.
 */
export function ProfileSection() {
  const { t } = useTranslation()
  const { user, setUser } = useAuthStore()
  const [editingUsername, setEditingUsername] = useState(false)
  const [usernameDraft, setUsernameDraft] = useState(user?.username ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)
  const [editingEmail, setEditingEmail] = useState(false)
  const [emailDraft, setEmailDraft] = useState('')
  const [emailError, setEmailError] = useState<string | null>(null)
  const [emailNotice, setEmailNotice] = useState<string | null>(null)

  const startEditUsername = () => {
    setUsernameDraft(user?.username ?? '')
    setError(null)
    setSuccess(null)
    setEditingUsername(true)
  }

  const cancelEditUsername = () => {
    setEditingUsername(false)
    setError(null)
  }

  const saveUsername = async () => {
    const next = usernameDraft.trim()
    if (!next) {
      setError(t('settings.profile.usernameRequired'))
      return
    }
    setBusy(true)
    setError(null)
    try {
      await apiClient.auth.setUsername(next)
      const me = await apiClient.auth.me()
      setUser(me)
      setSuccess(t('settings.profile.usernameSaved'))
      setEditingUsername(false)
    } catch (e) {
      if (e instanceof ApiRequestError && e.status === 409) {
        setError(t('settings.profile.usernameTaken'))
      } else {
        setError(t('settings.errorGeneric'))
      }
    } finally {
      setBusy(false)
    }
  }

  const submitEmail = async () => {
    const next = emailDraft.trim()
    if (!next.includes('@')) {
      setEmailError(t('settings.profile.emailInvalid'))
      return
    }
    setBusy(true)
    setEmailError(null)
    setEmailNotice(null)
    try {
      await apiClient.auth.requestEmailChange(next)
      setEmailNotice(t('settings.profile.emailChangeRequested'))
      setEditingEmail(false)
    } catch (e) {
      if (e instanceof ApiRequestError && e.status === 409) {
        setEmailError(t('settings.profile.emailTaken'))
      } else if (e instanceof ApiRequestError && e.body.code === 'EMAIL_INVALID') {
        setEmailError(t('settings.profile.emailInvalid'))
      } else {
        setEmailError(t('settings.errorGeneric'))
      }
    } finally {
      setBusy(false)
    }
  }

  const used = user?.used_bytes ?? 0
  const quota = user?.quota_bytes ?? 0
  const storageText = quota > 0
    ? `${formatBytes(used)} / ${formatBytes(quota)}`
    : `${formatBytes(used)} (${t('settings.profile.unlimited')})`
  const storagePercent = quota > 0 ? Math.min(100, Math.round((used / quota) * 100)) : null

  return (
    <SectionCard
      title={t('settings.profile.title')}
      description={t('settings.profile.description')}
      icon={<UserIcon className="h-4 w-4 text-muted-foreground" />}
    >
      <EditableField
        id="profile-username"
        label={t('settings.profile.username')}
        editing={editingUsername}
        busy={busy}
        value={user?.username ?? ''}
        draft={usernameDraft}
        onDraftChange={setUsernameDraft}
        onStartEdit={startEditUsername}
        onSave={saveUsername}
        onCancel={cancelEditUsername}
        editLabel={t('settings.profile.editUsername')}
      />

      <EditableField
        id="profile-email"
        label={t('settings.profile.email')}
        editing={editingEmail}
        busy={busy}
        type="email"
        value={user?.email ?? ''}
        draft={emailDraft}
        editPlaceholder={t('settings.profile.newEmail')}
        onDraftChange={setEmailDraft}
        onStartEdit={() => {
          setEmailDraft('')
          setEmailError(null)
          setEmailNotice(null)
          setEditingEmail(true)
        }}
        onSave={submitEmail}
        onCancel={() => { setEditingEmail(false); setEmailError(null) }}
        editLabel={t('settings.profile.editEmail')}
      />

      <div className="pt-4 mt-2 border-t border-border grid grid-cols-[auto_1fr] gap-x-6 gap-y-4 items-center">
        <span className="text-xs text-muted-foreground">{t('settings.profile.role')}</span>
        <span>
          <span className="inline-flex items-center rounded-full bg-primary/10 text-primary px-2.5 py-0.5 text-xs font-medium capitalize">
            {user?.role}
          </span>
        </span>
        <span className="text-xs text-muted-foreground self-start pt-0.5">{t('settings.profile.storage')}</span>
        <div className="space-y-1.5">
          <div className="text-sm font-medium">{storageText}</div>
          {storagePercent !== null && (
            <div className="h-1.5 w-full max-w-sm rounded-full bg-muted overflow-hidden">
              <div
                className="h-full bg-primary transition-all"
                style={{ width: `${storagePercent}%` }}
              />
            </div>
          )}
        </div>
      </div>

      {error && <Alert type="error" message={error} />}
      {success && <Alert type="success" message={success} />}
      {emailError && <Alert type="error" message={emailError} />}
      {emailNotice && <Alert type="success" message={emailNotice} />}
    </SectionCard>
  )
}
