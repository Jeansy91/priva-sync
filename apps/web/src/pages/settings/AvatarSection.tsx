import { useState } from 'react'
import { Button, cn } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { Image as ImageIcon, Trash2, Loader2, Upload } from 'lucide-react'
import { apiClient, useAuthStore } from '../../store/auth'
import { UserAvatar } from '../../components/layout/AppShell'
import { Alert, SectionCard } from './SettingsPrimitives'

/**
 * Avatar upload/delete card. Hard-caps client-side to 1 MiB to spare a
 * round-trip; server also enforces the same limit and returns 413 if
 * the file slips through (rotated jpeg etc.).
 */
export function AvatarSection() {
  const { t } = useTranslation()
  const { user, setUser } = useAuthStore()
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState<{ type: 'success' | 'error'; msg: string } | null>(null)

  const onPickFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file) return
    if (file.size > 1024 * 1024) {
      setStatus({ type: 'error', msg: t('settings.avatar.tooLarge') })
      return
    }
    setBusy(true)
    setStatus(null)
    try {
      await apiClient.auth.uploadAvatar(file)
      const me = await apiClient.auth.me()
      setUser(me)
      setStatus({ type: 'success', msg: t('settings.avatar.uploaded') })
    } catch (err: unknown) {
      const code = (err as { status?: number } | null)?.status
      const msg = code === 413
        ? t('settings.avatar.tooLarge')
        : code === 400
          ? t('settings.avatar.invalidImage')
          : t('settings.avatar.uploadFailed')
      setStatus({ type: 'error', msg })
    } finally {
      setBusy(false)
    }
  }

  const onDelete = async () => {
    setBusy(true)
    setStatus(null)
    try {
      await apiClient.auth.deleteAvatar()
      const me = await apiClient.auth.me()
      setUser(me)
      setStatus({ type: 'success', msg: t('settings.avatar.removed') })
    } catch {
      setStatus({ type: 'error', msg: t('settings.avatar.removeFailed') })
    } finally {
      setBusy(false)
    }
  }

  return (
    <SectionCard
      title={t('settings.avatar.title')}
      description={t('settings.avatar.description')}
      icon={<ImageIcon className="h-4 w-4 text-muted-foreground" />}
    >
      <div className="flex items-center gap-4">
        <UserAvatar user={user} size={64} />
        <div className="flex flex-wrap gap-2">
          <label>
            <input
              type="file"
              accept="image/png,image/jpeg"
              className="sr-only"
              onChange={onPickFile}
              disabled={busy}
            />
            <span
              className={cn(
                'inline-flex items-center gap-1.5 rounded-md border border-input bg-background px-3 py-1.5 text-sm font-medium hover:bg-accent cursor-pointer',
                busy && 'opacity-50 pointer-events-none'
              )}
            >
              {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Upload className="h-3.5 w-3.5" />}
              {t('settings.avatar.upload')}
            </span>
          </label>
          {user?.avatar_url && (
            <Button variant="ghost" size="sm" onClick={onDelete} disabled={busy}>
              <Trash2 className="h-3.5 w-3.5 mr-1.5" /> {t('settings.avatar.remove')}
            </Button>
          )}
        </div>
      </div>
      {status && <div className="mt-3"><Alert type={status.type} message={status.msg} /></div>}
    </SectionCard>
  )
}
