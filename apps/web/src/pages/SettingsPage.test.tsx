import { describe, it, expect, beforeEach, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../test/renderWithI18n'
import SettingsPage from './SettingsPage'
import { apiClient, useAuthStore } from '../store/auth'
import { ApiRequestError, type Me } from '@privasync/api-client'

const fakeUser: Me = {
  id: 1,
  username: 'alice',
  email: 'alice@example.com',
  role: 'user',
  totp_enabled: false,
  used_bytes: 1024,
  quota_bytes: 0,
  preferred_language: 'de',
  avatar_url: null,
  show_file_tree: true,
} as unknown as Me

beforeEach(() => {
  useAuthStore.setState({ user: fakeUser, accessToken: 't', isAuthenticated: true })
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

describe('SettingsPage — DE', () => {
  it('zeigt Profile-, Avatar-, Sprache-, 2FA- und Passwort-Sektionen', () => {
    const { getByRole, getByText } = renderWithI18n(<SettingsPage />, { lang: 'de' })
    expect(getByRole('heading', { level: 1, name: 'Einstellungen' })).toBeInTheDocument()
    expect(getByText('Profil')).toBeInTheDocument()
    expect(getByText('Profilbild')).toBeInTheDocument()
    expect(getByText('Sprache')).toBeInTheDocument()
    expect(getByText('Zwei-Faktor-Authentifizierung')).toBeInTheDocument()
    expect(getByText('Passwort ändern')).toBeInTheDocument()
  })

  it('gruppiert Sektionen unter Konto, Sicherheit und Darstellung', () => {
    const { getByRole } = renderWithI18n(<SettingsPage />, { lang: 'de' })
    expect(getByRole('heading', { level: 2, name: 'Konto' })).toBeInTheDocument()
    expect(getByRole('heading', { level: 2, name: 'Sicherheit' })).toBeInTheDocument()
    expect(getByRole('heading', { level: 2, name: 'Darstellung' })).toBeInTheDocument()
  })

  it('zeigt deutsche Profil-Labels', () => {
    const { getByText } = renderWithI18n(<SettingsPage />, { lang: 'de' })
    expect(getByText('Benutzername')).toBeInTheDocument()
    expect(getByText('Rolle')).toBeInTheDocument()
    expect(getByText('Speicher')).toBeInTheDocument()
  })
})

describe('SettingsPage — Benutzername ändern', () => {
  it('blendet bei Klick auf "Bearbeiten" ein Eingabefeld ein, ruft setUsername und aktualisiert den Store', async () => {
    const setUsername = vi.spyOn(apiClient.auth, 'setUsername').mockResolvedValue(undefined as any)
    vi.spyOn(apiClient.auth, 'me').mockResolvedValue({ ...fakeUser, username: 'bob' } as any)
    const u = userEvent.setup()

    const { findByRole, getByRole } = renderWithI18n(<SettingsPage />, { lang: 'de' })
    await u.click(await findByRole('button', { name: 'Benutzernamen ändern' }))

    const input = getByRole('textbox', { name: 'Benutzername' })
    await u.clear(input)
    await u.type(input, 'bob')
    await u.click(getByRole('button', { name: 'Speichern' }))

    await vi.waitFor(() => expect(setUsername).toHaveBeenCalledWith('bob'))
    await vi.waitFor(() => expect(useAuthStore.getState().user?.username).toBe('bob'))
  })

  it('zeigt Fehler bei 409-Konflikt', async () => {
    vi.spyOn(apiClient.auth, 'setUsername').mockRejectedValue(
      new ApiRequestError(409, { error: 'conflict', code: 'USERNAME_OR_EMAIL_TAKEN' })
    )
    const u = userEvent.setup()
    const { findByRole, getByRole, findByText } = renderWithI18n(<SettingsPage />, { lang: 'de' })
    await u.click(await findByRole('button', { name: 'Benutzernamen ändern' }))
    const input = getByRole('textbox', { name: 'Benutzername' })
    await u.clear(input)
    await u.type(input, 'taken_name')
    await u.click(getByRole('button', { name: 'Speichern' }))
    expect(await findByText('Benutzername bereits vergeben')).toBeInTheDocument()
  })
})

describe('SettingsPage — E-Mail ändern', () => {
  it('ruft requestEmailChange und zeigt Bestätigungs-Hinweis', async () => {
    const req = vi.spyOn(apiClient.auth, 'requestEmailChange').mockResolvedValue(undefined as any)
    const u = userEvent.setup()
    const { findByRole, getByRole, findByText } = renderWithI18n(<SettingsPage />, { lang: 'de' })

    await u.click(await findByRole('button', { name: 'E-Mail-Adresse ändern' }))
    const input = getByRole('textbox', { name: 'Neue E-Mail-Adresse' })
    await u.type(input, 'fresh@example.com')
    await u.click(getByRole('button', { name: 'Speichern' }))

    await vi.waitFor(() => expect(req).toHaveBeenCalledWith('fresh@example.com'))
    expect(await findByText('Bestätigungslink an die neue Adresse gesendet.')).toBeInTheDocument()
  })

  it('zeigt Fehler bei 409', async () => {
    vi.spyOn(apiClient.auth, 'requestEmailChange').mockRejectedValue(
      new ApiRequestError(409, { error: 'conflict', code: 'EMAIL_TAKEN' })
    )
    const u = userEvent.setup()
    const { findByRole, getByRole, findByText } = renderWithI18n(<SettingsPage />, { lang: 'de' })
    await u.click(await findByRole('button', { name: 'E-Mail-Adresse ändern' }))
    const input = getByRole('textbox', { name: 'Neue E-Mail-Adresse' })
    await u.type(input, 'taken@example.com')
    await u.click(getByRole('button', { name: 'Speichern' }))
    expect(await findByText('Diese E-Mail ist bereits vergeben')).toBeInTheDocument()
  })
})

describe('SettingsPage — Datei-Baum-Toggle', () => {
  it('toggelt show_file_tree und persistiert via apiClient', async () => {
    const setShow = vi.spyOn(apiClient.auth, 'setShowFileTree').mockResolvedValue(undefined as any)
    const u = userEvent.setup()

    const { findByLabelText } = renderWithI18n(<SettingsPage />, { lang: 'de' })
    const toggle = await findByLabelText('Datei-Baum anzeigen') as HTMLInputElement
    expect(toggle.checked).toBe(true)

    await u.click(toggle)

    await vi.waitFor(() => expect(setShow).toHaveBeenCalledWith(false))
    await vi.waitFor(() => expect(useAuthStore.getState().user?.show_file_tree).toBe(false))
  })
})

describe('SettingsPage — EN', () => {
  it('renders English section headings', () => {
    const { getByRole, getByText } = renderWithI18n(<SettingsPage />, { lang: 'en' })
    expect(getByRole('heading', { level: 1, name: 'Settings' })).toBeInTheDocument()
    expect(getByText('Account')).toBeInTheDocument()
    expect(getByText('Profile picture')).toBeInTheDocument()
    expect(getByText('Language')).toBeInTheDocument()
    expect(getByText('Two-factor authentication')).toBeInTheDocument()
    expect(getByText('Change password')).toBeInTheDocument()
  })

  it('renders English profile labels', () => {
    const { getByText } = renderWithI18n(<SettingsPage />, { lang: 'en' })
    expect(getByText('Username')).toBeInTheDocument()
    expect(getByText('Role')).toBeInTheDocument()
    expect(getByText('Storage')).toBeInTheDocument()
  })
})
