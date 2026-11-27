import { useState } from 'react'
import { Link, useSearchParams, useNavigate } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { ArrowLeft, KeyRound, Loader2, Mail } from 'lucide-react'
import { Button, Input, Label } from '@privasync/ui'
import { ApiRequestError } from '@privasync/api-client'
import { apiClient } from '../store/auth'
import { useTranslation } from '@privasync/i18n'

function makeRequestSchema(t: (k: string) => string) {
  return z.object({
    email: z.string().email(t('passwordReset.emailInvalid')),
  })
}

function makeConfirmSchema(t: (k: string) => string) {
  return z
    .object({
      password:        z.string().min(12, t('passwordReset.passwordMin')),
      passwordConfirm: z.string(),
    })
    .refine((d) => d.password === d.passwordConfirm, {
      message: t('passwordReset.passwordMismatch'),
      path:    ['passwordConfirm'],
    })
}

type RequestData = { email: string }
type ConfirmData = { password: string; passwordConfirm: string }

export default function PasswordResetPage() {
  const { t } = useTranslation()
  const [params] = useSearchParams()
  const token    = params.get('token')

  return (
    <div className="min-h-screen flex items-center justify-center bg-background p-4">
      <div className="w-full max-w-sm space-y-6">
        <div className="text-center space-y-2">
          <img src="/logo.png" alt="PrivaSync" className="mx-auto h-16 w-16" />
          <h1 className="text-2xl font-semibold tracking-tight">{t('passwordReset.title')}</h1>
        </div>

        {token ? <ConfirmForm token={token} /> : <RequestForm />}

        <div className="border-t border-border pt-4 text-center">
          <Link
            to="/login"
            className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="h-3 w-3" />
            {t('passwordReset.back')}
          </Link>
        </div>
      </div>
    </div>
  )
}

function RequestForm() {
  const { t } = useTranslation()
  const [sent, setSent] = useState(false)
  const form = useForm<RequestData>({ resolver: zodResolver(makeRequestSchema(t)) })

  const onSubmit = async (data: RequestData) => {
    try {
      await apiClient.auth.requestPasswordReset(data.email)
    } catch {
      // Silent — server ignores lookup errors for enumeration protection.
    }
    setSent(true)
  }

  if (sent) {
    return (
      <p className="text-sm rounded-md border border-border bg-muted/30 px-4 py-3 text-muted-foreground">
        {t('passwordReset.requestSent')}
      </p>
    )
  }

  return (
    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
      <p className="text-sm text-muted-foreground">
        {t('passwordReset.requestIntro')}
      </p>

      <div className="space-y-2">
        <Label htmlFor="email">{t('passwordReset.emailLabel')}</Label>
        <div className="relative">
          <Mail className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            id="email"
            type="email"
            placeholder={t('passwordReset.emailPlaceholder')}
            className="pl-9"
            autoComplete="email"
            {...form.register('email')}
          />
        </div>
        {form.formState.errors.email && (
          <p className="text-xs text-destructive">{form.formState.errors.email.message}</p>
        )}
      </div>

      <Button type="submit" className="w-full" disabled={form.formState.isSubmitting}>
        {form.formState.isSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
        {t('passwordReset.requestSubmit')}
      </Button>
    </form>
  )
}

function ConfirmForm({ token }: { token: string }) {
  const { t } = useTranslation()
  const navigate             = useNavigate()
  const [apiError, setError] = useState<string | null>(null)
  const [done, setDone]      = useState(false)
  const form = useForm<ConfirmData>({ resolver: zodResolver(makeConfirmSchema(t)) })

  const onSubmit = async (data: ConfirmData) => {
    setError(null)
    try {
      await apiClient.auth.confirmPasswordReset(token, data.password)
      setDone(true)
      setTimeout(() => navigate('/login', { replace: true }), 2000)
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.body.error : t('passwordReset.errorGeneric'))
    }
  }

  if (done) {
    return (
      <p className="text-sm rounded-md border border-border bg-muted/30 px-4 py-3 text-muted-foreground">
        {t('passwordReset.confirmDone')}
      </p>
    )
  }

  return (
    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
      <p className="text-sm text-muted-foreground">
        {t('passwordReset.confirmIntro')}
      </p>

      <div className="space-y-2">
        <Label htmlFor="password">{t('passwordReset.newPassword')}</Label>
        <div className="relative">
          <KeyRound className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            id="password"
            type="password"
            placeholder={t('passwordReset.newPasswordPlaceholder')}
            className="pl-9"
            autoComplete="new-password"
            {...form.register('password')}
          />
        </div>
        {form.formState.errors.password && (
          <p className="text-xs text-destructive">{form.formState.errors.password.message}</p>
        )}
      </div>

      <div className="space-y-2">
        <Label htmlFor="passwordConfirm">{t('passwordReset.confirmPassword')}</Label>
        <div className="relative">
          <KeyRound className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            id="passwordConfirm"
            type="password"
            placeholder={t('passwordReset.confirmPasswordPlaceholder')}
            className="pl-9"
            autoComplete="new-password"
            {...form.register('passwordConfirm')}
          />
        </div>
        {form.formState.errors.passwordConfirm && (
          <p className="text-xs text-destructive">{form.formState.errors.passwordConfirm.message}</p>
        )}
      </div>

      {apiError && (
        <p className="text-sm rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-destructive">{apiError}</p>
      )}

      <Button type="submit" className="w-full" disabled={form.formState.isSubmitting}>
        {form.formState.isSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
        {t('passwordReset.confirmSubmit')}
      </Button>
    </form>
  )
}
