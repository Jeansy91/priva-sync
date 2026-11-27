import { useState, useEffect, useCallback } from 'react'
import { Button, Label, Input } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { HardDrive, Loader2, Plus, Trash2 } from 'lucide-react'
import { apiClient } from '../../store/auth'
import type { AppPasswordInfo } from '@privasync/api-client'
import { Alert, SectionCard } from './SettingsPrimitives'

/**
 * App-password management. These are the WebDAV credentials: the account
 * password is not accepted there, because Basic Auth cannot ask for a second
 * factor (ADR-0040).
 *
 * Step machine like PasskeySection:
 *   idle    → list + "add" button
 *   name    → ask for a device label
 *   created → show the secret once; the server keeps only a hash
 */
export function AppPasswordSection() {
  const { t } = useTranslation()
  const [entries, setEntries] = useState<AppPasswordInfo[] | null>(null)
  const [status, setStatus] = useState<{ type: 'success' | 'error'; msg: string } | null>(null)
  const [step, setStep] = useState<'idle' | 'name' | 'created'>('idle')
  const [name, setName] = useState('')
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [deletingId, setDeletingId] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await apiClient.auth.listAppPasswords()
      setEntries(res.app_passwords)
    } catch {
      setEntries([])
    }
  }, [])

  useEffect(() => { load() }, [load])

  const errorMessage = (err: unknown, fallback: string) =>
    (err as { body?: { error?: string } } | null)?.body?.error ?? fallback

  const handleCreate = async () => {
    const label = name.trim()
    if (!label) return
    setBusy(true)
    setStatus(null)
    try {
      const created = await apiClient.auth.createAppPassword(label)
      setSecret(created.secret)
      setStep('created')
      setName('')
      await load()
    } catch (err: unknown) {
      setStatus({ type: 'error', msg: errorMessage(err, t('settings.appPassword.createError')) })
    } finally {
      setBusy(false)
    }
  }

  const handleDelete = async (id: string) => {
    if (!confirm(t('settings.appPassword.removeConfirm'))) return
    setDeletingId(id)
    setStatus(null)
    try {
      await apiClient.auth.deleteAppPassword(id)
      await load()
    } catch (err: unknown) {
      setStatus({ type: 'error', msg: errorMessage(err, t('settings.appPassword.removeError')) })
    } finally {
      setDeletingId(null)
    }
  }

  return (
    <SectionCard
      title={t('settings.appPassword.title')}
      description={t('settings.appPassword.description')}
      icon={<HardDrive className="h-4 w-4 text-muted-foreground" />}
    >
      {status && <Alert type={status.type} message={status.msg} />}

      {entries && entries.length > 0 && (
        <div className="space-y-2">
          {entries.map((ap) => (
            <div key={ap.id} className="flex items-center justify-between rounded-md border border-border px-4 py-3">
              <div className="text-sm">
                <p className="font-medium">{ap.name}</p>
                <p className="text-muted-foreground mt-0.5">
                  {ap.last_used_at
                    ? t('settings.appPassword.lastUsed', { date: new Date(ap.last_used_at).toLocaleDateString() })
                    : t('settings.appPassword.neverUsed')}
                </p>
              </div>
              <Button
                variant="outline"
                size="sm"
                onClick={() => handleDelete(ap.id)}
                disabled={deletingId === ap.id}
              >
                {deletingId === ap.id
                  ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  : <Trash2 className="h-3.5 w-3.5" />}
                {t('settings.appPassword.remove')}
              </Button>
            </div>
          ))}
        </div>
      )}

      {entries && entries.length === 0 && step === 'idle' && (
        <p className="text-sm text-muted-foreground">{t('settings.appPassword.empty')}</p>
      )}

      {step === 'idle' && (
        <Button variant="outline" onClick={() => { setName(''); setStep('name') }}>
          <Plus className="h-4 w-4" />
          {t('settings.appPassword.add')}
        </Button>
      )}

      {step === 'name' && (
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="app_password_name" className="text-xs text-muted-foreground">
              {t('settings.appPassword.nameLabel')}
            </Label>
            <Input
              id="app_password_name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('settings.appPassword.namePlaceholder')}
              autoFocus
            />
          </div>
          <div className="flex gap-2">
            <Button onClick={handleCreate} disabled={busy || !name.trim()}>
              {busy && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('settings.appPassword.create')}
            </Button>
            <Button type="button" variant="ghost" onClick={() => setStep('idle')}>
              {t('settings.appPassword.cancel')}
            </Button>
          </div>
        </div>
      )}

      {step === 'created' && (
        <div className="space-y-3">
          <Alert type="success" message={t('settings.appPassword.createdOnce')} />
          <code className="block select-all break-all rounded-md border border-border bg-muted px-4 py-3 font-mono text-sm">
            {secret}
          </code>
          <Button variant="outline" onClick={() => { setSecret(''); setStep('idle') }}>
            {t('settings.appPassword.done')}
          </Button>
        </div>
      )}
    </SectionCard>
  )
}
