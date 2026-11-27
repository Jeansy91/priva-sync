import { useForm, Controller } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Loader2 } from 'lucide-react'
import { Button, Label, OtpInput } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'

export type TotpFormData = { code: string }

function makeSchema(t: (k: string) => string) {
  return z.object({
    code: z.string()
      .length(6, t('auth.errors.totpCodeLength'))
      .regex(/^\d+$/, t('auth.errors.totpCodeDigits')),
  })
}

interface Props {
  onSubmit: (data: TotpFormData) => Promise<void>
  onUseBackup: () => void
  onBack: () => void
  apiError: string | null
}

export default function TotpForm({ onSubmit, onUseBackup, onBack, apiError }: Props) {
  const { t } = useTranslation()
  const form = useForm<TotpFormData>({ resolver: zodResolver(makeSchema(t)) })

  return (
    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="code">{t('auth.authenticatorCode')}</Label>
        <Controller
          control={form.control}
          name="code"
          defaultValue=""
          render={({ field }) => (
            <OtpInput
              id="code"
              name={field.name}
              value={field.value}
              onChange={field.onChange}
              onBlur={field.onBlur}
              onComplete={() => form.handleSubmit(onSubmit)()}
              autoFocus
              hasError={!!form.formState.errors.code}
              ariaLabel={t('auth.authenticatorCode')}
              className="justify-center"
            />
          )}
        />
        {form.formState.errors.code && (
          <p className="text-xs text-destructive">{form.formState.errors.code.message}</p>
        )}
      </div>

      {apiError && (
        <p className="text-sm rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-destructive">{apiError}</p>
      )}

      <Button type="submit" className="w-full" disabled={form.formState.isSubmitting}>
        {form.formState.isSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
        {t('auth.confirmCode')}
      </Button>

      <Button
        type="button"
        variant="link"
        size="sm"
        className="w-full text-xs text-muted-foreground hover:text-foreground"
        onClick={onUseBackup}
      >
        {t('auth.useBackupCode')}
      </Button>

      <Button type="button" variant="ghost" className="w-full" onClick={onBack}>
        {t('auth.backToLogin')}
      </Button>
    </form>
  )
}
