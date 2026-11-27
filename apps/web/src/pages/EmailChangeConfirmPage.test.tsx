import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderWithI18n } from '../test/renderWithI18n'
import EmailChangeConfirmPage from './EmailChangeConfirmPage'
import { apiClient, useAuthStore } from '../store/auth'
import { ApiRequestError, type Me } from '@privasync/api-client'

beforeEach(() => {
  useAuthStore.setState({ user: null, accessToken: null, isAuthenticated: false })
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

const meStub: Me = {
  id: 1,
  username: 'a',
  email: 'new@x',
  role: 'user',
  totp_enabled: false,
  used_bytes: 0,
  quota_bytes: 0,
  preferred_language: 'de',
  avatar_url: null,
} as unknown as Me

describe('EmailChangeConfirmPage', () => {
  it('ruft confirmEmailChange mit Token aus Query und zeigt Erfolg', async () => {
    const confirm = vi.spyOn(apiClient.auth, 'confirmEmailChange').mockResolvedValue(undefined)
    vi.spyOn(apiClient.auth, 'me').mockResolvedValue(meStub)

    const { findByText } = renderWithI18n(<EmailChangeConfirmPage />, {
      lang: 'de', route: '/email-change/confirm?token=abc-123',
    })

    expect(await findByText('E-Mail-Adresse erfolgreich geändert.')).toBeInTheDocument()
    expect(confirm).toHaveBeenCalledWith('abc-123')
  })

  it('zeigt Fehler bei fehlendem Token', async () => {
    const { findByText } = renderWithI18n(<EmailChangeConfirmPage />, {
      lang: 'de', route: '/email-change/confirm',
    })
    expect(await findByText('Bestätigungslink ist ungültig oder abgelaufen.')).toBeInTheDocument()
  })

  it('zeigt Fehler bei Reject', async () => {
    vi.spyOn(apiClient.auth, 'confirmEmailChange').mockRejectedValue(
      new ApiRequestError(400, { error: 'bad', code: 'TOKEN_INVALID_OR_EXPIRED' })
    )
    const { findByText } = renderWithI18n(<EmailChangeConfirmPage />, {
      lang: 'de', route: '/email-change/confirm?token=expired',
    })
    expect(await findByText('Bestätigungslink ist ungültig oder abgelaufen.')).toBeInTheDocument()
  })
})
