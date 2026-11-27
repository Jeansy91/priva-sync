import {
  ApiRequestError,
  type ApiError,
  type AppPasswordCreated,
  type AppPasswordListResponse,
  type BackupCodesListResponse,
  type BackupCodesResponse,
  type LoginRequest,
  type LoginResponse,
  type Me,
  type PasskeyCredentialInfo,
  type PasskeyLoginBeginResponse,
  type PasskeyLoginFinishRequest,
  type PasskeyRegisterBeginResponse,
  type PasskeyRegisterFinishRequest,
  type TOTPSetupResponse,
  type TokenResponse,
  type Verify2FARequest,
} from '../types'
import type { HttpClient } from './http'

export function makeAuthApi(http: HttpClient) {
  return {
    login: (body: LoginRequest) =>
      http.request<LoginResponse>('POST', '/auth/login', { body }),

    verify2FA: (body: Verify2FARequest) =>
      http.request<TokenResponse>('POST', '/auth/2fa', { body }),

    refresh: () =>
      http.request<TokenResponse>('POST', '/auth/refresh'),

    logout: () =>
      http.request<void>('POST', '/auth/logout'),

    me: () =>
      http.request<Me>('GET', '/auth/me'),

    changePassword: (currentPassword: string, newPassword: string) =>
      http.request<void>('PUT', '/auth/password', {
        body: { current_password: currentPassword, new_password: newPassword },
      }),

    requestPasswordReset: (email: string) =>
      http.request<void>('POST', '/auth/password/reset-request', { body: { email } }),

    confirmPasswordReset: (token: string, newPassword: string) =>
      http.request<void>('POST', '/auth/password/reset-confirm', {
        body: { token, new_password: newPassword },
      }),

    setupTOTP: () =>
      http.request<TOTPSetupResponse>('POST', '/auth/totp/setup'),

    confirmTOTP: (code: string) =>
      http.request<BackupCodesResponse>('POST', '/auth/totp/confirm', { body: { code } }),

    disableTOTP: () =>
      http.request<void>('DELETE', '/auth/totp'),

    listBackupCodes: () =>
      http.request<BackupCodesListResponse>('GET', '/auth/totp/backup-codes'),

    regenerateBackupCodes: () =>
      http.request<BackupCodesResponse>('POST', '/auth/totp/backup-codes/regenerate'),

    passkeyRegisterBegin: () =>
      http.request<PasskeyRegisterBeginResponse>('POST', '/auth/passkey/register/begin'),

    passkeyRegisterFinish: (body: PasskeyRegisterFinishRequest) =>
      http.request<PasskeyCredentialInfo>('POST', '/auth/passkey/register/finish', { body }),

    passkeyLoginBegin: () =>
      http.request<PasskeyLoginBeginResponse>('POST', '/auth/passkey/login/begin'),

    passkeyLoginFinish: (body: PasskeyLoginFinishRequest) =>
      http.request<TokenResponse>('POST', '/auth/passkey/login/finish', { body }),

    listPasskeys: () =>
      http.request<PasskeyCredentialInfo[]>('GET', '/auth/passkey'),

    deletePasskey: (id: string) =>
      http.request<void>('DELETE', `/auth/passkey/${id}`),

    // WebDAV credentials (ADR-0040). createAppPassword returns the plaintext
    // secret exactly once; it is never retrievable afterwards.
    createAppPassword: (name: string) =>
      http.request<AppPasswordCreated>('POST', '/auth/app-passwords', { body: { name } }),

    listAppPasswords: () =>
      http.request<AppPasswordListResponse>('GET', '/auth/app-passwords'),

    deleteAppPassword: (id: string) =>
      http.request<void>('DELETE', `/auth/app-passwords/${id}`),

    uploadAvatar: async (file: Blob): Promise<{ avatar_url: string }> => {
      const form = new FormData()
      form.append('avatar', file)
      const res = await fetch(`${http.absoluteBase()}/api/v1/auth/avatar`, {
        method: 'PUT',
        headers: http.authHeaders(),
        credentials: 'include',
        body: form,
      })
      if (!res.ok) {
        let body: ApiError = { error: res.statusText }
        try { body = await res.json() } catch { /* keep statusText */ }
        throw new ApiRequestError(res.status, body)
      }
      return res.json()
    },

    deleteAvatar: () =>
      http.request<void>('DELETE', '/auth/avatar'),

    setLanguage: (language: 'de' | 'en') =>
      http.request<void>('PUT', '/auth/language', { body: { language } }),

    setShowFileTree: (show: boolean) =>
      http.request<void>('PUT', '/auth/preferences/file-tree', { body: { show } }),

    setShowFileCheckboxes: (show: boolean) =>
      http.request<void>('PUT', '/auth/preferences/file-checkboxes', { body: { show } }),

    setTrashView: (view: 'list' | 'tree') =>
      http.request<void>('PUT', '/auth/preferences/trash-view', { body: { view } }),

    setUsername: (username: string) =>
      http.request<void>('PUT', '/auth/profile/username', { body: { username } }),

    requestEmailChange: (newEmail: string) =>
      http.request<void>('POST', '/auth/profile/email-change', { body: { new_email: newEmail } }),

    confirmEmailChange: (token: string) =>
      http.request<void>('POST', '/auth/profile/email-change/confirm', { body: { token } }),

    fetchAvatar: async (userID: string): Promise<Blob | null> => {
      const res = await fetch(`${http.absoluteBase()}/api/v1/auth/avatar/${userID}`, {
        headers:     http.authHeaders(),
        credentials: 'include',
      })
      if (res.status === 404) return null
      if (!res.ok) throw new ApiRequestError(res.status, { error: res.statusText })
      return res.blob()
    },
  }
}

export type AuthApi = ReturnType<typeof makeAuthApi>
