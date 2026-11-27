import { useState, useEffect } from 'react'
import { useForm, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Button, Label, OtpInput } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import {
  Shield, ShieldCheck, ShieldOff, Loader2, RefreshCw, Smartphone,
} from 'lucide-react'
import QRCode from 'react-qr-code'
import { apiClient, useAuthStore } from '../../store/auth'
import type { TOTPSetupResponse } from '@privasync/api-client'
import { Alert, SectionCard } from './SettingsPrimitives'
import { BackupCodesDisplay } from './BackupCodes'

function makeTotpConfirmSchema(t: (k: string) => string) {
  return z.object({
    code: z.string().length(6, t('settings.totp.codeRequired'))
      .regex(/^\d+$/, t('settings.totp.codeDigitsOnly')),
  })
}

type TOTPConfirmForm = { code: string }

/**
 * TOTP/2FA management section. Four-step flow:
 *   idle    → buttons to setup (inactive) or regenerate codes (active)
 *   setup   → QR + manual key, click "next"
 *   confirm → 6-digit OTP form; success transitions to "codes"
 *   codes   → backup-codes display, exactly once
 */
export function TOTPSection() {
  const { t } = useTranslation()
  const { user, setUser } = useAuthStore()
  const [step, setStep] = useState<'idle' | 'setup' | 'confirm' | 'codes'>('idle')
  const [setupData, setSetupData] = useState<TOTPSetupResponse | null>(null)
  const [status, setStatus] = useState<{ type: 'success' | 'error'; msg: string } | null>(null)
  const [disableLoading, setDisableLoading] = useState(false)
  const [newCodes, setNewCodes] = useState<string[] | null>(null)
  const [unused, setUnused] = useState<number | null>(null)
  const [total, setTotal] = useState<number | null>(null)
  const [regenLoading, setRegenLoading] = useState(false)

  const confirmForm = useForm<TOTPConfirmForm>({
    resolver: zodResolver(makeTotpConfirmSchema(t)),
  })

  const loadCodeStats = async () => {
    try {
      const r = await apiClient.auth.listBackupCodes()
      setUnused(r.unused)
      setTotal(r.total)
    } catch {
      setUnused(null)
      setTotal(null)
    }
  }

  useEffect(() => {
    if (user?.totp_enabled && step === 'idle' && !newCodes) {
      loadCodeStats()
    }
  }, [user?.totp_enabled, step, newCodes])

  const handleSetup = async () => {
    setStatus(null)
    try {
      const data = await apiClient.auth.setupTOTP()
      setSetupData(data)
      setStep('setup')
    } catch (err: unknown) {
      const msg = (err as { body?: { error?: string } } | null)?.body?.error
      setStatus({ type: 'error', msg: msg ?? t('settings.totp.setupError') })
    }
  }

  const handleConfirm = async (data: TOTPConfirmForm) => {
    setStatus(null)
    try {
      const r = await apiClient.auth.confirmTOTP(data.code)
      if (user) setUser({ ...user, totp_enabled: true })
      setNewCodes(r.backup_codes)
      setSetupData(null)
      setStep('codes')
    } catch (err: unknown) {
      const msg = (err as { body?: { error?: string } } | null)?.body?.error
      setStatus({ type: 'error', msg: msg ?? t('settings.totp.invalidCode') })
    }
  }

  const handleDisable = async () => {
    setDisableLoading(true)
    setStatus(null)
    try {
      await apiClient.auth.disableTOTP()
      if (user) setUser({ ...user, totp_enabled: false })
      setUnused(null)
      setTotal(null)
      setStatus({ type: 'success', msg: t('settings.totp.deactivated') })
    } catch (err: unknown) {
      const msg = (err as { body?: { error?: string } } | null)?.body?.error
      setStatus({ type: 'error', msg: msg ?? t('settings.totp.deactivateError') })
    } finally {
      setDisableLoading(false)
    }
  }

  const handleRegenerate = async () => {
    if (!confirm(t('settings.totp.regenerateConfirm'))) return
    setRegenLoading(true)
    setStatus(null)
    try {
      const r = await apiClient.auth.regenerateBackupCodes()
      setNewCodes(r.backup_codes)
      setStep('codes')
    } catch (err: unknown) {
      const msg = (err as { body?: { error?: string } } | null)?.body?.error
      setStatus({ type: 'error', msg: msg ?? t('settings.totp.regenerateError') })
    } finally {
      setRegenLoading(false)
    }
  }

  const handleCodesDone = () => {
    setNewCodes(null)
    setStep('idle')
    setStatus({ type: 'success', msg: t('settings.totp.codesSaved') })
  }

  return (
    <SectionCard
      title={t('settings.totp.title')}
      description={
        user?.totp_enabled
          ? t('settings.totp.descriptionActive')
          : t('settings.totp.descriptionInactive')
      }
      icon={
        user?.totp_enabled
          ? <ShieldCheck className="h-4 w-4 text-green-600" />
          : <Shield className="h-4 w-4 text-muted-foreground" />
      }
    >
      {status && <Alert type={status.type} message={status.msg} />}

      {/* Active state */}
      {user?.totp_enabled && step === 'idle' && (
        <div className="space-y-3">
          <div className="flex items-center justify-between rounded-md border border-green-500/40 bg-green-50/30 dark:bg-green-950/20 px-4 py-3">
            <div className="flex items-center gap-2 text-sm text-green-700 dark:text-green-400">
              <ShieldCheck className="h-4 w-4" />
              {t('settings.totp.active')}
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={handleDisable}
              disabled={disableLoading}
            >
              {disableLoading
                ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                : <ShieldOff className="h-3.5 w-3.5" />}
              {t('settings.totp.deactivate')}
            </Button>
          </div>
          <div className="flex items-center justify-between rounded-md border border-border px-4 py-3">
            <div className="text-sm">
              <p className="font-medium">{t('settings.totp.backupCodes')}</p>
              <p className="text-muted-foreground mt-0.5">
                {unused !== null && total !== null
                  ? t('settings.totp.backupCodesUnused', { unused, total })
                  : t('settings.totp.backupCodesIdle')}
              </p>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={handleRegenerate}
              disabled={regenLoading}
            >
              {regenLoading
                ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                : <RefreshCw className="h-3.5 w-3.5" />}
              {t('settings.totp.regenerate')}
            </Button>
          </div>
        </div>
      )}

      {/* Newly generated codes — shown once */}
      {step === 'codes' && newCodes && user && (
        <BackupCodesDisplay
          codes={newCodes}
          username={user.username}
          onDone={handleCodesDone}
        />
      )}

      {/* Inactive state */}
      {!user?.totp_enabled && step === 'idle' && (
        <Button variant="outline" onClick={handleSetup}>
          <Smartphone className="h-4 w-4" />
          {t('settings.totp.setup')}
        </Button>
      )}

      {/* Setup: show QR + secret */}
      {step === 'setup' && setupData && (
        <div className="space-y-4">
          <div className="rounded-md border border-border p-4 space-y-3">
            <p className="text-sm">{t('settings.totp.scanQr')}</p>
            <div className="flex justify-center rounded-md bg-white p-4">
              <QRCode value={setupData.otpauth_url} size={192} />
            </div>
            <p className="text-sm text-muted-foreground">{t('settings.totp.manualKey')}</p>
            <p className="font-mono text-sm tracking-widest select-all bg-muted rounded-sm px-3 py-2">
              {setupData.secret}
            </p>
          </div>

          <Button
            variant="outline"
            size="sm"
            onClick={() => { setStep('confirm'); confirmForm.reset() }}
          >
            {t('settings.totp.next')}
          </Button>
        </div>
      )}

      {/* Confirm TOTP code */}
      {step === 'confirm' && (
        <form onSubmit={confirmForm.handleSubmit(handleConfirm)} className="space-y-4">
          <div className="space-y-1">
            <Label htmlFor="totp_code" className="text-xs text-muted-foreground">{t('settings.totp.codeLabel')}</Label>
            <Controller
              control={confirmForm.control}
              name="code"
              defaultValue=""
              render={({ field }) => (
                <OtpInput
                  id="totp_code"
                  name={field.name}
                  value={field.value}
                  onChange={field.onChange}
                  onBlur={field.onBlur}
                  onComplete={() => confirmForm.handleSubmit(handleConfirm)()}
                  autoFocus
                  hasError={!!confirmForm.formState.errors.code}
                  ariaLabel={t('settings.totp.codeLabel')}
                />
              )}
            />
            {confirmForm.formState.errors.code && (
              <p className="text-xs text-destructive">{confirmForm.formState.errors.code.message}</p>
            )}
          </div>
          <div className="flex gap-2">
            <Button type="submit" disabled={confirmForm.formState.isSubmitting}>
              {confirmForm.formState.isSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('settings.totp.confirmActivate')}
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => { setStep('setup'); setStatus(null) }}
            >
              {t('settings.totp.back')}
            </Button>
          </div>
        </form>
      )}
    </SectionCard>
  )
}
