import { useEffect, useState, lazy, Suspense } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { apiClient, useAuthStore } from './store/auth'
import { AppShell } from './components/layout/AppShell'
import LoginPage from './pages/LoginPage'
import DownloadPage from './pages/DownloadPage'
import PasswordResetPage from './pages/PasswordResetPage'
import SetupPage from './pages/SetupPage'

// Lazy-loaded Pages
const FilesPage     = lazy(() => import('./pages/FilesPage'))
const ConflictsPage = lazy(() => import('./pages/ConflictsPage'))
const SharesPage    = lazy(() => import('./pages/SharesPage'))
const TrashPage     = lazy(() => import('./pages/TrashPage'))
const SettingsPage  = lazy(() => import('./pages/SettingsPage'))
const EmailChangeConfirmPage = lazy(() => import('./pages/EmailChangeConfirmPage'))
const PublicSharePage = lazy(() => import('./pages/PublicSharePage'))
const AdminRoutes   = lazy(() => import('./pages/admin/AdminRoutes').then(m => ({ default: m.AdminRoutes })))

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, accessToken, setTokens, setUser, logout } = useAuthStore()
  // Wait for the token probe before redirecting to the login page.
  const [checking, setChecking] = useState(() => !!accessToken && !isAuthenticated)

  useEffect(() => {
    if (!accessToken || isAuthenticated) {
      setChecking(false)
      return
    }
    apiClient.setAccessToken(accessToken)
    apiClient.auth.me()
      .then((user) => {
        setUser(user)
        setTokens(accessToken)
      })
      .catch(() => logout())
      .finally(() => setChecking(false))
  }, [])

  if (checking) {
    return (
      <div className="flex items-center justify-center h-dvh bg-background">
        <div className="h-6 w-6 animate-spin rounded-full border-2 border-muted border-t-primary" />
      </div>
    )
  }

  if (!isAuthenticated) {
    return <Navigate to="/login" replace />
  }
  return <>{children}</>
}

function RequireAdmin({ children }: { children: React.ReactNode }) {
  const { user } = useAuthStore()
  if (user?.role !== 'admin') return <Navigate to="/" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/setup"          element={<SetupPage />} />
        <Route path="/login"          element={<LoginPage />} />
        <Route path="/password-reset" element={<PasswordResetPage />} />
        <Route path="/download"       element={<DownloadPage />} />
        <Route path="/s/:token" element={
          <Suspense fallback={<div className="p-8 text-muted-foreground text-sm">Laden…</div>}>
            <PublicSharePage />
          </Suspense>
        } />

        <Route
          path="/*"
          element={
            <RequireAuth>
              <AppShell>
                <Suspense fallback={<div className="p-8 text-muted-foreground text-sm">Laden…</div>}>
                  <Routes>
                    <Route path="/"          element={<Navigate to="/files" replace />} />
                    <Route path="/files/*"   element={<FilesPage />} />
                    <Route path="/conflicts" element={<ConflictsPage />} />
                    <Route path="/shares"    element={<SharesPage />} />
                    <Route path="/trash"     element={<TrashPage />} />
                    <Route path="/settings"  element={<SettingsPage />} />
                    <Route path="/email-change/confirm" element={<EmailChangeConfirmPage />} />
                    <Route path="/admin/*"   element={<RequireAdmin><AdminRoutes /></RequireAdmin>} />
                    <Route path="*"          element={<Navigate to="/files" replace />} />
                  </Routes>
                </Suspense>
              </AppShell>
            </RequireAuth>
          }
        />
      </Routes>
    </BrowserRouter>
  )
}
