import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Loader2 } from 'lucide-react'
import { Button, Input, Label } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'

export type BackupFormData = { code: string }

function makeSchema(t: (k: string) => string) {
  return z.object({
    code: z.string().min(1, t('auth.errors.backupCodeRequired')),
  })
}

interface Props {
  onSubmit: (data: BackupFormData) => Promise<void>
  onUseAuthenticator: () => void
  onBack: () => void
  apiError: string | null
}

export default function BackupForm({ onSubmit, onUseAuthenticator, onBack, apiError }: Props) {
  const { t } = useTranslation()
  const form = useForm<BackupFormData>({ resolver: zodResolver(makeSchema(t)) })

  return (
    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="backup_code">{t('auth.backupCode')}</Label>
        <Input
          id="backup_code"
          placeholder={t('auth.backupCodePlaceholder')}
          autoComplete="one-time-code"
          className="text-center font-mono tracking-widest"
          {...form.register('code')}
        />
        {form.formState.errors.code && (
          <p className="text-xs text-destructive">{form.formState.errors.code.message}</p>
        )}
        <p className="text-xs text-muted-foreground">{t('auth.backupCodeHint')}</p>
      </div>

      {apiError && (
        <p className="text-sm rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-destructive">{apiError}</p>
      )}

      <Button type="submit" className="w-full" disabled={form.formState.isSubmitting}>
        {form.formState.isSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
        {t('auth.redeemCode')}
      </Button>

      <Button
        type="button"
        variant="link"
        size="sm"
        className="w-full text-xs text-muted-foreground hover:text-foreground"
        onClick={onUseAuthenticator}
      >
        {t('auth.useAuthenticatorCode')}
      </Button>

      <Button type="button" variant="ghost" className="w-full" onClick={onBack}>
        {t('auth.backToLogin')}
      </Button>
    </form>
  )
}
