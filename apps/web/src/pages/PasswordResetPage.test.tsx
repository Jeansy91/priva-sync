import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiRequestError } from '@privasync/api-client'
import { renderWithI18n } from '../test/renderWithI18n'
import { apiClient } from '../store/auth'
import PasswordResetPage from './PasswordResetPage'

beforeEach(() => {
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

describe('PasswordResetPage — DE (request)', () => {
  it('rendert Header und E-Mail-Feld', () => {
    const { getByRole, getByLabelText } = renderWithI18n(<PasswordResetPage />, {
      lang: 'de', route: '/reset-password',
    })
    expect(getByRole('heading', { level: 1, name: 'Passwort zurücksetzen' })).toBeInTheDocument()
    expect(getByLabelText('E-Mail')).toBeInTheDocument()
    expect(getByRole('button', { name: /Reset-Link senden/i })).toBeInTheDocument()
  })
})

describe('PasswordResetPage — EN (request)', () => {
  it('renders header and email field', () => {
    const { getByRole, getByLabelText } = renderWithI18n(<PasswordResetPage />, {
      lang: 'en', route: '/reset-password',
    })
    expect(getByRole('heading', { level: 1, name: 'Reset password' })).toBeInTheDocument()
    expect(getByLabelText('Email')).toBeInTheDocument()
    expect(getByRole('button', { name: /Send reset link/i })).toBeInTheDocument()
  })
})

describe('PasswordResetPage — request form behaviour', () => {
  it('validates the email format before submitting', async () => {
    const requestSpy = vi.spyOn(apiClient.auth, 'requestPasswordReset')
    const u = userEvent.setup()
    const { getByLabelText, findByText, container } = renderWithI18n(<PasswordResetPage />, { route: '/password-reset' })

    await u.type(getByLabelText('E-Mail'), 'not-an-email')
    // fireEvent.submit dispatches the submit event directly, bypassing the
    // native type="email" constraint validation a real button click would
    // trigger first (jsdom blocks that click before React's handler runs).
    fireEvent.submit(container.querySelector('form')!)

    expect(await findByText('Gültige E-Mail-Adresse erforderlich')).toBeInTheDocument()
    expect(requestSpy).not.toHaveBeenCalled()
  })

  it('shows the confirmation even when the server call fails (enumeration protection)', async () => {
    vi.spyOn(apiClient.auth, 'requestPasswordReset').mockRejectedValue(new Error('unknown account'))
    const u = userEvent.setup()
    const { getByLabelText, getByRole, findByText } = renderWithI18n(<PasswordResetPage />, { route: '/password-reset' })

    await u.type(getByLabelText('E-Mail'), 'alice@example.com')
    await u.click(getByRole('button', { name: 'Reset-Link senden' }))

    expect(await findByText(/Reset-Link verschickt/)).toBeInTheDocument()
  })

  it('shows the confirmation on a successful request', async () => {
    vi.spyOn(apiClient.auth, 'requestPasswordReset').mockResolvedValue(undefined as never)
    const u = userEvent.setup()
    const { getByLabelText, getByRole, findByText } = renderWithI18n(<PasswordResetPage />, { route: '/password-reset' })

    await u.type(getByLabelText('E-Mail'), 'alice@example.com')
    await u.click(getByRole('button', { name: 'Reset-Link senden' }))

    expect(await findByText(/Reset-Link verschickt/)).toBeInTheDocument()
    expect(apiClient.auth.requestPasswordReset).toHaveBeenCalledWith('alice@example.com')
  })

  it('shows the back-to-login link', () => {
    const { getByRole } = renderWithI18n(<PasswordResetPage />, { route: '/password-reset' })
    expect(getByRole('link', { name: /Zurück zur Anmeldung/ })).toBeInTheDocument()
  })
})

describe('PasswordResetPage — confirm form (with token)', () => {
  const route = '/password-reset?token=tok-123'

  it('validates password length and match', async () => {
    const u = userEvent.setup()
    const { getByLabelText, getByRole, findByText } = renderWithI18n(<PasswordResetPage />, { route })

    await u.type(getByLabelText('Neues Passwort'), 'short')
    await u.type(getByLabelText('Passwort bestätigen'), 'short')
    await u.click(getByRole('button', { name: 'Passwort setzen' }))

    expect(await findByText('Mindestens 12 Zeichen')).toBeInTheDocument()
  })

  it('rejects mismatched passwords', async () => {
    const u = userEvent.setup()
    const { getByLabelText, getByRole, findByText } = renderWithI18n(<PasswordResetPage />, { route })

    await u.type(getByLabelText('Neues Passwort'), 'aaaaaaaaaaaa')
    await u.type(getByLabelText('Passwort bestätigen'), 'bbbbbbbbbbbb')
    await u.click(getByRole('button', { name: 'Passwort setzen' }))

    expect(await findByText('Passwörter stimmen nicht überein')).toBeInTheDocument()
  })

  it('confirms the reset and shows the done message', async () => {
    vi.spyOn(apiClient.auth, 'confirmPasswordReset').mockResolvedValue(undefined as never)
    const u = userEvent.setup()
    const { getByLabelText, getByRole, findByText } = renderWithI18n(<PasswordResetPage />, { route })

    await u.type(getByLabelText('Neues Passwort'), 'aaaaaaaaaaaa')
    await u.type(getByLabelText('Passwort bestätigen'), 'aaaaaaaaaaaa')
    await u.click(getByRole('button', { name: 'Passwort setzen' }))

    expect(await findByText(/Passwort wurde aktualisiert/)).toBeInTheDocument()
    expect(apiClient.auth.confirmPasswordReset).toHaveBeenCalledWith('tok-123', 'aaaaaaaaaaaa')
  })

  it('shows the server error message from an ApiRequestError', async () => {
    vi.spyOn(apiClient.auth, 'confirmPasswordReset').mockRejectedValue(
      new ApiRequestError(400, { error: 'Token abgelaufen' }),
    )
    const u = userEvent.setup()
    const { getByLabelText, getByRole, findByText } = renderWithI18n(<PasswordResetPage />, { route })

    await u.type(getByLabelText('Neues Passwort'), 'aaaaaaaaaaaa')
    await u.type(getByLabelText('Passwort bestätigen'), 'aaaaaaaaaaaa')
    await u.click(getByRole('button', { name: 'Passwort setzen' }))

    expect(await findByText('Token abgelaufen')).toBeInTheDocument()
  })

  it('shows a generic error for a non-API failure', async () => {
    vi.spyOn(apiClient.auth, 'confirmPasswordReset').mockRejectedValue(new Error('boom'))
    const u = userEvent.setup()
    const { getByLabelText, getByRole, findByText } = renderWithI18n(<PasswordResetPage />, { route })

    await u.type(getByLabelText('Neues Passwort'), 'aaaaaaaaaaaa')
    await u.type(getByLabelText('Passwort bestätigen'), 'aaaaaaaaaaaa')
    await u.click(getByRole('button', { name: 'Passwort setzen' }))

    expect(await findByText('Unbekannter Fehler')).toBeInTheDocument()
  })
})
