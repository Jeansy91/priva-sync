import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { KeyRound, Loader2, Lock, Mail, User } from 'lucide-react'
import { Button, Input, Label } from '@privasync/ui'
import { ApiRequestError } from '@privasync/api-client'
import { apiClient } from '../store/auth'
import { useTranslation } from '@privasync/i18n'
import PreLoginLanguagePicker from '../components/PreLoginLanguagePicker'

function makeSetupSchema(t: (k: string) => string) {
  return z.object({
    token:    z.string().min(1, t('setup.tokenRequired')),
    username: z.string().min(1, t('setup.usernameRequired')),
    email:    z.string().email(t('setup.emailInvalid')),
    password: z.string().min(8, t('setup.passwordMin')),
  })
}

type SetupFormData = { token: string; username: string; email: string; password: string }

export default function SetupPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [apiError, setApiError] = useState<string | null>(null)
  const [checking, setChecking] = useState(true)

  const form = useForm<SetupFormData>({ resolver: zodResolver(makeSetupSchema(t)) })

  useEffect(() => {
    apiClient.setup.status()
      .then((s) => {
        if (!s.needs_setup) navigate('/login', { replace: true })
      })
      .catch(() => {})
      .finally(() => setChecking(false))
  }, [navigate])

  const onSubmit = async (data: SetupFormData) => {
    setApiError(null)
    try {
      await apiClient.setup.complete(data)
      navigate('/login', { replace: true })
    } catch (err) {
      setApiError(err instanceof ApiRequestError ? err.body.error : t('setup.errorGeneric'))
    }
  }

  if (checking) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-background">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    )
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-background p-4">
      <div className="absolute right-4 top-4">
        <PreLoginLanguagePicker />
      </div>
      <div className="w-full max-w-sm space-y-6">
        <div className="text-center space-y-2">
          <img src="/logo.png" alt="PrivaSync" className="mx-auto h-16 w-16" />
          <h1 className="text-2xl font-semibold tracking-tight">{t('setup.title')}</h1>
          <p className="text-sm text-muted-foreground">
            {t('setup.subtitle')}
          </p>
        </div>

        <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="token">{t('setup.tokenLabel')}</Label>
            <div className="relative">
              <KeyRound className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
              <Input
                id="token"
                placeholder={t('setup.tokenPlaceholder')}
                className="pl-9 font-mono text-xs"
                autoComplete="off"
                {...form.register('token')}
              />
            </div>
            {form.formState.errors.token && (
              <p className="text-xs text-destructive">{form.formState.errors.token.message}</p>
            )}
          </div>

          <div className="space-y-2">
            <Label htmlFor="username">{t('setup.usernameLabel')}</Label>
            <div className="relative">
              <User className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
              <Input
                id="username"
                placeholder={t('setup.usernamePlaceholder')}
                className="pl-9"
                autoComplete="username"
                {...form.register('username')}
              />
            </div>
            {form.formState.errors.username && (
              <p className="text-xs text-destructive">{form.formState.errors.username.message}</p>
            )}
          </div>

          <div className="space-y-2">
            <Label htmlFor="email">{t('setup.emailLabel')}</Label>
            <div className="relative">
              <Mail className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
              <Input
                id="email"
                type="email"
                placeholder={t('setup.emailPlaceholder')}
                className="pl-9"
                autoComplete="email"
                {...form.register('email')}
              />
            </div>
            {form.formState.errors.email && (
              <p className="text-xs text-destructive">{form.formState.errors.email.message}</p>
            )}
          </div>

          <div className="space-y-2">
            <Label htmlFor="password">{t('setup.passwordLabel')}</Label>
            <div className="relative">
              <Lock className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
              <Input
                id="password"
                type="password"
                placeholder={t('setup.passwordPlaceholder')}
                className="pl-9"
                autoComplete="new-password"
                {...form.register('password')}
              />
            </div>
            {form.formState.errors.password && (
              <p className="text-xs text-destructive">{form.formState.errors.password.message}</p>
            )}
          </div>

          {apiError && (
            <p className="text-sm rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-destructive">{apiError}</p>
          )}

          <Button type="submit" className="w-full" disabled={form.formState.isSubmitting}>
            {form.formState.isSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
            {t('setup.submit')}
          </Button>
        </form>
      </div>
    </div>
  )
}
