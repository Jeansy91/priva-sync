import { Link } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Loader2, Lock, User } from 'lucide-react'
import { Button, Input, Label } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'

export type LoginFormData = { username: string; password: string }

function makeSchema(t: (k: string) => string) {
  return z.object({
    username: z.string().min(1, t('auth.errors.usernameRequired')),
    password: z.string().min(1, t('auth.errors.passwordRequired')),
  })
}

interface Props {
  onSubmit: (data: LoginFormData) => Promise<void>
  apiError: string | null
}

export default function LoginForm({ onSubmit, apiError }: Props) {
  const { t } = useTranslation()
  const form = useForm<LoginFormData>({ resolver: zodResolver(makeSchema(t)) })

  return (
    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="username">{t('auth.username')}</Label>
        <div className="relative">
          <User className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            id="username"
            placeholder={t('auth.usernamePlaceholder')}
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
        <Label htmlFor="password">{t('auth.password')}</Label>
        <div className="relative">
          <Lock className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            id="password"
            type="password"
            placeholder={t('auth.passwordPlaceholder')}
            className="pl-9"
            autoComplete="current-password"
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
        {form.formState.isSubmitting ? t('auth.loggingIn') : t('auth.login')}
      </Button>

      <div className="text-center">
        <Link to="/password-reset" className="text-xs text-muted-foreground hover:text-foreground">
          {t('auth.forgotPassword')}
        </Link>
      </div>
    </form>
  )
}
