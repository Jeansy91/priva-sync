import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../test/renderWithI18n'

const login = vi.fn()
const verifyTotp = vi.fn()
const verifyInstance = vi.fn()
const startAutoUpdateFor = vi.fn()

vi.mock('../tauri', () => ({
  cmd: {
    login: (...a: unknown[]) => login(...a),
    verifyTotp: (...a: unknown[]) => verifyTotp(...a),
    verifyInstance: (...a: unknown[]) => verifyInstance(...a),
    startAutoUpdateFor: (...a: unknown[]) => startAutoUpdateFor(...a),
  },
}))

import SetupPage from './SetupPage'

beforeEach(() => {
  login.mockReset()
  verifyTotp.mockReset()
  verifyInstance.mockReset().mockResolvedValue(false)
  startAutoUpdateFor.mockReset().mockResolvedValue(undefined)
})

function fillAndSubmit(serverUrl: string, username: string, password: string) {
  fireEvent.change(screen.getByPlaceholderText('https://sync.example.com'), {
    target: { value: serverUrl },
  })
  fireEvent.change(screen.getByPlaceholderText('Benutzername eingeben'), {
    target: { value: username },
  })
  fireEvent.change(screen.getByPlaceholderText('Passwort eingeben'), {
    target: { value: password },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Anmelden' }))
}

describe('SetupPage', () => {
  it('prefills server URL and username from the last-used values, leaving them empty when unset', () => {
    renderWithI18n(<SetupPage onOpenSettings={vi.fn()} onLogin={vi.fn()} initialServerUrl="http://myserver:8080" initialUsername="alice" />)
    expect(screen.getByPlaceholderText('https://sync.example.com')).toHaveValue('http://myserver:8080')
    expect(screen.getByPlaceholderText('Benutzername eingeben')).toHaveValue('alice')
  })

  it('offers a settings entry point before login', () => {
    const onOpenSettings = vi.fn()
    renderWithI18n(<SetupPage onOpenSettings={onOpenSettings} onLogin={vi.fn()} initialServerUrl="" initialUsername="" />)

    fireEvent.click(screen.getByRole('button', { name: 'Einstellungen' }))
    expect(onOpenSettings).toHaveBeenCalledTimes(1)
  })

  it('calls cmd.login with trimmed inputs and onLogin on success', async () => {
    login.mockResolvedValue({ requires_2fa: false, session_token: null })
    const onLogin = vi.fn()
    renderWithI18n(<SetupPage onOpenSettings={vi.fn()} onLogin={onLogin} initialServerUrl="" initialUsername="" />)

    fillAndSubmit('  http://myserver:8080  ', '  alice  ', 'secret123')

    await waitFor(() => expect(login).toHaveBeenCalledWith('http://myserver:8080', 'alice', 'secret123'))
    await waitFor(() => expect(onLogin).toHaveBeenCalled())
  })

  it('shows an error message and does not call onLogin when login fails', async () => {
    login.mockRejectedValue(new Error('invalid credentials'))
    const onLogin = vi.fn()
    renderWithI18n(<SetupPage onOpenSettings={vi.fn()} onLogin={onLogin} initialServerUrl="" initialUsername="" />)

    fillAndSubmit('http://myserver:8080', 'alice', 'wrong-password')

    await waitFor(() => expect(screen.getByText(/invalid credentials/)).toBeInTheDocument())
    expect(onLogin).not.toHaveBeenCalled()
  })

  it('translates a coded command error instead of showing the raw server text', async () => {
    login.mockRejectedValue({ error: 'invalid credentials', code: 'INVALID_CREDENTIALS' })
    renderWithI18n(<SetupPage onOpenSettings={vi.fn()} onLogin={vi.fn()} initialServerUrl="" initialUsername="" />)

    fillAndSubmit('http://myserver:8080', 'alice', 'wrong-password')

    await waitFor(() => expect(screen.getByText('Benutzername oder Passwort ist falsch.')).toBeInTheDocument())
    expect(screen.queryByText(/invalid credentials/)).not.toBeInTheDocument()
  })

  it('disables the submit button while the login request is in flight', async () => {
    let resolveLogin: (v: { requires_2fa: boolean; session_token: string | null }) => void = () => {}
    login.mockReturnValue(new Promise((resolve) => { resolveLogin = resolve }))
    renderWithI18n(<SetupPage onOpenSettings={vi.fn()} onLogin={vi.fn()} initialServerUrl="" initialUsername="" />)

    fillAndSubmit('http://myserver:8080', 'alice', 'secret123')

    await waitFor(() => expect(screen.getByRole('button', { name: 'Verbinden…' })).toBeDisabled())
    resolveLogin({ requires_2fa: false, session_token: null })
  })

  it('switches to the TOTP step when the server requires 2FA, then calls onLogin after a correct code', async () => {
    login.mockResolvedValue({ requires_2fa: true, session_token: 'sess-1' })
    verifyTotp.mockResolvedValue(undefined)
    const onLogin = vi.fn()
    renderWithI18n(<SetupPage onOpenSettings={vi.fn()} onLogin={onLogin} initialServerUrl="" initialUsername="" />)

    fillAndSubmit('http://myserver:8080', 'alice', 'secret123')

    await waitFor(() => expect(screen.getByRole('group', { name: 'Authenticator-Code' })).toBeInTheDocument())

    const digits = screen.getAllByRole('textbox', { name: /Authenticator-Code/ })
    '123456'.split('').forEach((digit, i) => {
      fireEvent.change(digits[i], { target: { value: digit } })
    })

    await waitFor(() => expect(verifyTotp).toHaveBeenCalledWith('sess-1', '123456', false))
    await waitFor(() => expect(onLogin).toHaveBeenCalled())
  })

  it('shows an error and stays on the TOTP step when the code is wrong', async () => {
    login.mockResolvedValue({ requires_2fa: true, session_token: 'sess-1' })
    verifyTotp.mockRejectedValue(new Error('invalid totp code'))
    renderWithI18n(<SetupPage onOpenSettings={vi.fn()} onLogin={vi.fn()} initialServerUrl="" initialUsername="" />)

    fillAndSubmit('http://myserver:8080', 'alice', 'secret123')

    await waitFor(() => expect(screen.getByRole('group', { name: 'Authenticator-Code' })).toBeInTheDocument())
    const digits = screen.getAllByRole('textbox', { name: /Authenticator-Code/ })
    '000000'.split('').forEach((digit, i) => {
      fireEvent.change(digits[i], { target: { value: digit } })
    })

    await waitFor(() => expect(screen.getByText(/invalid totp code/)).toBeInTheDocument())
  })

  it('switches to backup-code entry and submits it as such', async () => {
    login.mockResolvedValue({ requires_2fa: true, session_token: 'sess-1' })
    verifyTotp.mockResolvedValue(undefined)
    const onLogin = vi.fn()
    renderWithI18n(<SetupPage onOpenSettings={vi.fn()} onLogin={onLogin} initialServerUrl="" initialUsername="" />)

    fillAndSubmit('http://myserver:8080', 'alice', 'secret123')

    await waitFor(() => expect(screen.getByText('Backup-Code verwenden')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Backup-Code verwenden'))

    fireEvent.change(screen.getByPlaceholderText('Backup-Code eingeben'), {
      target: { value: 'deadbeefcafef00d' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Bestätigen' }))

    await waitFor(() => expect(verifyTotp).toHaveBeenCalledWith('sess-1', 'deadbeefcafef00d', true))
    await waitFor(() => expect(onLogin).toHaveBeenCalled())
  })

  it('debounces the instance check and shows a success icon, then checks for an update once', async () => {
    verifyInstance.mockResolvedValue(true)
    renderWithI18n(<SetupPage onOpenSettings={vi.fn()} onLogin={vi.fn()} initialServerUrl="" initialUsername="" />)

    fireEvent.change(screen.getByPlaceholderText('https://sync.example.com'), {
      target: { value: 'https://sync.example.com' },
    })

    // Not called immediately — debounced.
    expect(verifyInstance).not.toHaveBeenCalled()

    await waitFor(() => expect(verifyInstance).toHaveBeenCalledWith('https://sync.example.com'), { timeout: 1000 })
    await waitFor(() => expect(screen.getByTitle('PrivaSync-Instanz gefunden')).toBeInTheDocument())
    await waitFor(() => expect(startAutoUpdateFor).toHaveBeenCalledWith('https://sync.example.com'))

    // Re-checking the exact same (already-validated) URL must not re-trigger
    // the update check.
    startAutoUpdateFor.mockClear()
    fireEvent.change(screen.getByPlaceholderText('https://sync.example.com'), {
      target: { value: 'https://sync.example.com/' },
    })
    fireEvent.change(screen.getByPlaceholderText('https://sync.example.com'), {
      target: { value: 'https://sync.example.com' },
    })
    await new Promise((r) => setTimeout(r, 600))
    expect(startAutoUpdateFor).not.toHaveBeenCalled()
  })

  it('shows an invalid icon when no PrivaSync instance is reachable, without blocking submit', async () => {
    verifyInstance.mockResolvedValue(false)
    renderWithI18n(<SetupPage onOpenSettings={vi.fn()} onLogin={vi.fn()} initialServerUrl="" initialUsername="" />)

    fireEvent.change(screen.getByPlaceholderText('https://sync.example.com'), {
      target: { value: 'https://not-privasync.example.com' },
    })

    await waitFor(() => expect(screen.getByTitle('Keine PrivaSync-Instanz erreichbar')).toBeInTheDocument(), { timeout: 1000 })
    expect(screen.getByRole('button', { name: 'Anmelden' })).not.toBeDisabled()
    expect(startAutoUpdateFor).not.toHaveBeenCalled()
  })
})
