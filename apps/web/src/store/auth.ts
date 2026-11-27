import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { ApiClient, ApiRequestError, type Me, type RefreshResult } from '@privasync/api-client'
import { changeLanguage } from '@privasync/i18n'

const BASE_URL = import.meta.env.VITE_API_URL ?? ''

export const apiClient = new ApiClient(BASE_URL)

// On 401: try refreshing the token first (uses the HttpOnly refresh_token cookie).
// Only a 401 from /auth/refresh itself ends the session — a transport error or
// a 5xx is temporary and must not log the user out (ADR-0046).
apiClient.setRefreshFn(async (): Promise<RefreshResult> => {
  try {
    const { access_token } = await apiClient.auth.refresh()
    if (!access_token) return { status: 'unauthorized' }
    // Through the store, not just the client: the token is persisted and every
    // consumer of it (SSE, rehydration after a reload) sees the new one.
    useAuthStore.getState().setTokens(access_token)
    return { status: 'refreshed', accessToken: access_token }
  } catch (err) {
    if (err instanceof ApiRequestError && err.status === 401) return { status: 'unauthorized' }
    return { status: 'unavailable' }
  }
})

apiClient.setOnUnauthorized(() => {
  useAuthStore.getState().logout()
})

interface AuthState {
  user: Me | null
  accessToken: string | null
  isAuthenticated: boolean

  setTokens: (accessToken: string) => void
  setUser: (user: Me) => void
  logout: () => void
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      user: null,
      accessToken: null,
      isAuthenticated: false,

      setTokens: (accessToken) => {
        apiClient.setAccessToken(accessToken)
        set({ accessToken, isAuthenticated: true })
      },

      setUser: (user) => {
        // Sprache an die Server-Praeferenz angleichen, sobald /me beantwortet ist.
        // '' = never explicitly set -> keep the browser/OS detection from main.tsx.
        if (user.preferred_language !== '') {
          changeLanguage(user.preferred_language).catch(() => {})
        }
        set({ user })
      },

      logout: () => {
        apiClient.setAccessToken(null)
        set({ user: null, accessToken: null, isAuthenticated: false })
      },
    }),
    {
      name: 'privasync-auth',
      partialize: (state) => ({ accessToken: state.accessToken }),
      onRehydrateStorage: () => (state) => {
        if (state?.accessToken) {
          apiClient.setAccessToken(state.accessToken)
        }
      },
    }
  )
)
