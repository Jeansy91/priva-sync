import { useEffect, useState } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import { Loader2 } from 'lucide-react'
import { Button } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { apiClient, useAuthStore } from '../store/auth'

type State = 'pending' | 'success' | 'error'

// Confirmation page hit from the email-change verification link. Sits behind
// RequireAuth in App.tsx because the backend expects a valid access token —
// rules out cross-account confirms by construction.
export default function EmailChangeConfirmPage() {
  const { t } = useTranslation()
  const [params] = useSearchParams()
  const token = params.get('token')
  const navigate = useNavigate()
  const setUser = useAuthStore((s) => s.setUser)
  const [state, setState] = useState<State>('pending')

  useEffect(() => {
    if (!token) {
      setState('error')
      return
    }
    apiClient.auth.confirmEmailChange(token)
      .then(() => apiClient.auth.me())
      .then((me) => { setUser(me); setState('success') })
      .catch(() => setState('error'))
  }, [token, setUser])

  return (
    <div className="min-h-screen flex items-center justify-center bg-background p-4">
      <div className="w-full max-w-sm space-y-6 text-center">
        <h1 className="text-2xl font-semibold tracking-tight">
          {t('settings.profile.editEmail')}
        </h1>
        {state === 'pending' && (
          <p className="flex items-center justify-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" /> {t('common.loading')}
          </p>
        )}
        {state === 'success' && (
          <>
            <p className="rounded-md border border-green-500/40 bg-green-50/30 px-4 py-3 text-sm text-green-700 dark:text-green-400">
              {t('settings.profile.emailChanged')}
            </p>
            <Button onClick={() => navigate('/settings')}>
              {t('common.back')}
            </Button>
          </>
        )}
        {state === 'error' && (
          <>
            <p className="rounded-md border border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive">
              {t('settings.profile.emailChangeTokenInvalid')}
            </p>
            <Button variant="outline" onClick={() => navigate('/settings')}>
              {t('common.back')}
            </Button>
          </>
        )}
      </div>
    </div>
  )
}
