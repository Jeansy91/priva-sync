import { useState, useEffect, useCallback } from 'react'
import { Button, Label, Input } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { KeyRound, Loader2, Plus, Trash2 } from 'lucide-react'
import { apiClient } from '../../store/auth'
import type { PasskeyCredentialInfo } from '@privasync/api-client'
import { Alert, SectionCard } from './SettingsPrimitives'
import { createPasskey, passkeysSupported, PasskeyCancelledError } from '../../lib/webauthn'

/**
 * Passkey management section. Mirrors TOTPSection's step-machine idiom:
 *   idle → list + "add" button
 *   name → confirm the credential's display name, then persist it
 */
export function PasskeySection() {
  const { t } = useTranslation()
  const [passkeys, setPasskeys] = useState<PasskeyCredentialInfo[] | null>(null)
  const [status, setStatus] = useState<{ type: 'success' | 'error'; msg: string } | null>(null)
  const [step, setStep] = useState<'idle' | 'name'>('idle')
  const [pending, setPending] = useState<{ challengeToken: string; credential: unknown } | null>(null)
  const [name, setName] = useState('')
  const [addLoading, setAddLoading] = useState(false)
  const [deletingId, setDeletingId] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setPasskeys(await apiClient.auth.listPasskeys())
    } catch {
      setPasskeys([])
    }
  }, [])

  useEffect(() => { load() }, [load])

  const errorMessage = (err: unknown, fallback: string) =>
    (err as { body?: { error?: string } } | null)?.body?.error ?? fallback

  const handleAdd = async () => {
    setStatus(null)
    setAddLoading(true)
    try {
      const begin = await apiClient.auth.passkeyRegisterBegin()
      const credential = await createPasskey(begin.options)
      setPending({ challengeToken: begin.challenge_token, credential })
      setName('')
      setStep('name')
    } catch (err: unknown) {
      if (!(err instanceof PasskeyCancelledError)) {
        setStatus({ type: 'error', msg: errorMessage(err, t('settings.passkey.addError')) })
      }
    } finally {
      setAddLoading(false)
    }
  }

  const handleConfirmName = async () => {
    if (!pending) return
    setAddLoading(true)
    setStatus(null)
    try {
      await apiClient.auth.passkeyRegisterFinish({
        challenge_token: pending.challengeToken,
        credential: pending.credential,
        name: name.trim() || t('settings.passkey.defaultName'),
      })
      setPending(null)
      setStep('idle')
      setStatus({ type: 'success', msg: t('settings.passkey.added') })
      await load()
    } catch (err: unknown) {
      setStatus({ type: 'error', msg: errorMessage(err, t('settings.passkey.addError')) })
    } finally {
      setAddLoading(false)
    }
  }

  const handleDelete = async (id: string) => {
    if (!confirm(t('settings.passkey.removeConfirm'))) return
    setDeletingId(id)
    setStatus(null)
    try {
      await apiClient.auth.deletePasskey(id)
      await load()
    } catch (err: unknown) {
      setStatus({ type: 'error', msg: errorMessage(err, t('settings.passkey.removeError')) })
    } finally {
      setDeletingId(null)
    }
  }

  return (
    <SectionCard
      title={t('settings.passkey.title')}
      description={t('settings.passkey.description')}
      icon={<KeyRound className="h-4 w-4 text-muted-foreground" />}
    >
      {status && <Alert type={status.type} message={status.msg} />}

      {!passkeysSupported() && (
        <p className="text-sm text-muted-foreground">{t('settings.passkey.notSupported')}</p>
      )}

      {passkeys && passkeys.length > 0 && (
        <div className="space-y-2">
          {passkeys.map((p) => (
            <div key={p.id} className="flex items-center justify-between rounded-md border border-border px-4 py-3">
              <div className="text-sm">
                <p className="font-medium">{p.name}</p>
                <p className="text-muted-foreground mt-0.5">
                  {p.last_used_at
                    ? t('settings.passkey.lastUsed', { date: new Date(p.last_used_at).toLocaleDateString() })
                    : t('settings.passkey.neverUsed')}
                </p>
              </div>
              <Button
                variant="outline"
                size="sm"
                onClick={() => handleDelete(p.id)}
                disabled={deletingId === p.id}
              >
                {deletingId === p.id
                  ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  : <Trash2 className="h-3.5 w-3.5" />}
                {t('settings.passkey.remove')}
              </Button>
            </div>
          ))}
        </div>
      )}

      {passkeys && passkeys.length === 0 && step === 'idle' && (
        <p className="text-sm text-muted-foreground">{t('settings.passkey.empty')}</p>
      )}

      {passkeysSupported() && step === 'idle' && (
        <Button variant="outline" onClick={handleAdd} disabled={addLoading}>
          {addLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}
          {t('settings.passkey.add')}
        </Button>
      )}

      {step === 'name' && (
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="passkey_name" className="text-xs text-muted-foreground">
              {t('settings.passkey.nameLabel')}
            </Label>
            <Input
              id="passkey_name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t('settings.passkey.defaultName')}
              autoFocus
            />
          </div>
          <div className="flex gap-2">
            <Button onClick={handleConfirmName} disabled={addLoading}>
              {addLoading && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('settings.passkey.save')}
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => { setPending(null); setStep('idle') }}
            >
              {t('settings.passkey.cancel')}
            </Button>
          </div>
        </div>
      )}
    </SectionCard>
  )
}
