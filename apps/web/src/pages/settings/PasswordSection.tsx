import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Button, Input, Label } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { KeyRound, Loader2 } from 'lucide-react'
import { apiClient } from '../../store/auth'
import { Alert, SectionCard } from './SettingsPrimitives'

function makePasswordSchema(t: (k: string) => string) {
  return z.object({
    current_password: z.string().min(1, t('settings.password.required')),
    new_password: z.string().min(12, t('settings.password.minLength')),
    confirm_password: z.string(),
  }).refine((d) => d.new_password === d.confirm_password, {
    message: t('settings.password.mismatch'),
    path: ['confirm_password'],
  })
}

type PasswordForm = { current_password: string; new_password: string; confirm_password: string }

/**
 * Password-change form for the Security group. Validates current vs new
 * password match and minimum length before hitting the API.
 */
export function PasswordSection() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<{ type: 'success' | 'error'; msg: string } | null>(null)
  const form = useForm<PasswordForm>({ resolver: zodResolver(makePasswordSchema(t)) })

  const onSubmit = async (data: PasswordForm) => {
    setStatus(null)
    try {
      await apiClient.auth.changePassword(data.current_password, data.new_password)
      setStatus({ type: 'success', msg: t('settings.password.success') })
      form.reset()
    } catch (err: unknown) {
      const msg = (err as { body?: { error?: string } } | null)?.body?.error
      setStatus({ type: 'error', msg: msg ?? t('settings.password.errorGeneric') })
    }
  }

  return (
    <SectionCard
      title={t('settings.password.title')}
      description={t('settings.password.description')}
      icon={<KeyRound className="h-4 w-4 text-muted-foreground" />}
    >
      <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
        <div className="space-y-1">
          <Label htmlFor="current_password" className="text-xs text-muted-foreground">{t('settings.password.current')}</Label>
          <Input
            id="current_password"
            type="password"
            placeholder={t('settings.password.currentPlaceholder')}
            autoComplete="current-password"
            {...form.register('current_password')}
          />
          {form.formState.errors.current_password && (
            <p className="text-xs text-destructive">{form.formState.errors.current_password.message}</p>
          )}
        </div>
        <div className="space-y-1">
          <Label htmlFor="new_password" className="text-xs text-muted-foreground">{t('settings.password.new')}</Label>
          <Input
            id="new_password"
            type="password"
            placeholder={t('settings.password.newPlaceholder')}
            autoComplete="new-password"
            {...form.register('new_password')}
          />
          {form.formState.errors.new_password && (
            <p className="text-xs text-destructive">{form.formState.errors.new_password.message}</p>
          )}
        </div>
        <div className="space-y-1">
          <Label htmlFor="confirm_password" className="text-xs text-muted-foreground">{t('settings.password.confirm')}</Label>
          <Input
            id="confirm_password"
            type="password"
            placeholder={t('settings.password.confirmPlaceholder')}
            autoComplete="new-password"
            {...form.register('confirm_password')}
          />
          {form.formState.errors.confirm_password && (
            <p className="text-xs text-destructive">{form.formState.errors.confirm_password.message}</p>
          )}
        </div>

        {status && <Alert type={status.type} message={status.msg} />}

        <Button type="submit" disabled={form.formState.isSubmitting}>
          {form.formState.isSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
          {t('settings.password.submit')}
        </Button>
      </form>
    </SectionCard>
  )
}
