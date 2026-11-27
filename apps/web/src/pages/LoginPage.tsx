import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Download, Fingerprint, Loader2 } from 'lucide-react'
import { ApiRequestError } from '@privasync/api-client'
import { useTranslation, translateApiError } from '@privasync/i18n'
import { Button } from '@privasync/ui'
import { apiClient, useAuthStore } from '../store/auth'
import PreLoginLanguagePicker from '../components/PreLoginLanguagePicker'
import { getPasskey, passkeysSupported, PasskeyCancelledError } from '../lib/webauthn'
import LoginForm, { type LoginFormData } from './login/LoginForm'
import TotpForm, { type TotpFormData } from './login/TotpForm'
import BackupForm, { type BackupFormData } from './login/BackupForm'

export default function LoginPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { setTokens, setUser } = useAuthStore()
  const [sessionToken, setSessionToken] = useState<string | null>(null)
  const [apiError, setApiError] = useState<string | null>(null)
  const [useBackup, setUseBackup] = useState(false)
  const [passkeyLoading, setPasskeyLoading] = useState(false)

  useEffect(() => {
    apiClient.setup.status()
      .then((s) => { if (s.needs_setup) navigate('/setup', { replace: true }) })
      // Setup status is best-effort: if the probe fails the user stays on
      // the login page — login still works once setup is done.
      .catch(() => {})
  }, [navigate])

  const finalizeLogin = async (accessToken: string) => {
    setTokens(accessToken)
    const me = await apiClient.auth.me()
    setUser(me)
    navigate('/', { replace: true })
  }

  const toErrorMessage = (err: unknown) =>
    err instanceof ApiRequestError ? translateApiError(err.body) : t('auth.unknownError')

  const handleLogin = async (data: LoginFormData) => {
    setApiError(null)
    try {
      const res = await apiClient.auth.login(data)
      if (res.requires_2fa && res.session_token) {
        setSessionToken(res.session_token)
        return
      }
      if (res.access_token) await finalizeLogin(res.access_token)
    } catch (err) {
      setApiError(toErrorMessage(err))
    }
  }

  const handlePasskeyLogin = async () => {
    setApiError(null)
    setPasskeyLoading(true)
    try {
      const begin = await apiClient.auth.passkeyLoginBegin()
      const credential = await getPasskey(begin.options)
      const res = await apiClient.auth.passkeyLoginFinish({
        challenge_token: begin.challenge_token,
        credential,
      })
      await finalizeLogin(res.access_token)
    } catch (err) {
      if (!(err instanceof PasskeyCancelledError)) {
        setApiError(toErrorMessage(err))
      }
    } finally {
      setPasskeyLoading(false)
    }
  }

  const handleTOTP = async (data: TotpFormData) => {
    if (!sessionToken) return
    setApiError(null)
    try {
      const res = await apiClient.auth.verify2FA({ session_token: sessionToken, totp_code: data.code })
      await finalizeLogin(res.access_token)
    } catch (err) {
      setApiError(toErrorMessage(err))
    }
  }

  const handleBackup = async (data: BackupFormData) => {
    if (!sessionToken) return
    setApiError(null)
    try {
      const res = await apiClient.auth.verify2FA({ session_token: sessionToken, backup_code: data.code })
      await finalizeLogin(res.access_token)
    } catch (err) {
      setApiError(toErrorMessage(err))
    }
  }

  const resetToLogin = () => {
    setSessionToken(null)
    setApiError(null)
    setUseBackup(false)
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-background p-4">
      <div className="absolute right-4 top-4">
        <PreLoginLanguagePicker />
      </div>
      <div className="w-full max-w-sm space-y-6">
        <div className="text-center space-y-2">
          <img src="/logo.png" alt="PrivaSync" className="mx-auto h-16 w-16" />
          <h1 className="text-2xl font-semibold tracking-tight">PrivaSync</h1>
          <p className="text-sm text-muted-foreground">
            {sessionToken ? t('auth.twoFactorCode') : t('auth.loginSubtitle')}
          </p>
        </div>

        {!sessionToken && (
          <>
            <LoginForm onSubmit={handleLogin} apiError={apiError} />
            {passkeysSupported() && (
              <Button
                type="button"
                variant="outline"
                className="w-full"
                onClick={handlePasskeyLogin}
                disabled={passkeyLoading}
              >
                {passkeyLoading
                  ? <Loader2 className="h-4 w-4 animate-spin" />
                  : <Fingerprint className="h-4 w-4" />}
                {t('auth.passkeyLogin')}
              </Button>
            )}
            <div className="border-t border-border pt-4 text-center">
              <Link
                to="/download"
                className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
              >
                <Download className="h-3 w-3" />
                {t('auth.downloadDesktop')}
              </Link>
            </div>
          </>
        )}

        {sessionToken && !useBackup && (
          <TotpForm
            onSubmit={handleTOTP}
            onUseBackup={() => { setUseBackup(true); setApiError(null) }}
            onBack={resetToLogin}
            apiError={apiError}
          />
        )}

        {sessionToken && useBackup && (
          <BackupForm
            onSubmit={handleBackup}
            onUseAuthenticator={() => { setUseBackup(false); setApiError(null) }}
            onBack={resetToLogin}
            apiError={apiError}
          />
        )}
      </div>
    </div>
  )
}
