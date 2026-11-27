import { describe, it, expect, beforeEach, vi } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/renderWithI18n'
import { apiClient, useAuthStore } from '../../store/auth'
import { TOTPSection } from './TOTPSection'

function setUser(over: Record<string, unknown> = {}) {
  useAuthStore.setState({
    user: {
      id: 'u-1', username: 'alice', email: 'a@x', role: 'user',
      totp_enabled: false, quota_bytes: 0, used_bytes: 0, trash_bytes: 0,
      avatar_url: null, preferred_language: 'de',
      ...over,
    },
  } as any)
}

beforeEach(() => {
  vi.restoreAllMocks()
  setUser()
})

describe('TOTPSection — inactive state', () => {
  it('shows the setup button', () => {
    const { getByRole } = renderWithI18n(<TOTPSection />)
    expect(getByRole('button', { name: /Zwei-Faktor-Authentifizierung einrichten|einrichten/i })).toBeInTheDocument()
  })

  it('walks through setup -> confirm -> codes on a valid code', async () => {
    vi.spyOn(apiClient.auth, 'setupTOTP').mockResolvedValue({
      secret: 'ABCD1234', otpauth_url: 'otpauth://totp/x',
    })
    vi.spyOn(apiClient.auth, 'confirmTOTP').mockResolvedValue({
      backup_codes: ['aaaa-bbbb', 'cccc-dddd'],
    } as never)
    const u = userEvent.setup()
    const { getByRole, findByText, container } = renderWithI18n(<TOTPSection />)

    await u.click(getByRole('button', { name: /einrichten/i }))
    expect(await findByText('ABCD1234')).toBeInTheDocument()

    await u.click(getByRole('button', { name: /Weiter|next/i }))

    const inputs = container.querySelectorAll('input')
    expect(inputs).toHaveLength(6)
    '123456'.split('').forEach((digit, i) => {
      fireEvent.change(inputs[i], { target: { value: digit } })
    })

    await vi.waitFor(() => expect(apiClient.auth.confirmTOTP).toHaveBeenCalledWith('123456'))
    expect(await findByText('aaaa-bbbb')).toBeInTheDocument()
  })

  it('shows a setup error when the API call fails', async () => {
    vi.spyOn(apiClient.auth, 'setupTOTP').mockRejectedValue({ body: { error: 'server down' } })
    const u = userEvent.setup()
    const { getByRole, findByText } = renderWithI18n(<TOTPSection />)

    await u.click(getByRole('button', { name: /einrichten/i }))

    expect(await findByText('server down')).toBeInTheDocument()
  })
})

describe('TOTPSection — active state', () => {
  it('shows the deactivate and regenerate buttons, and loads code stats', async () => {
    vi.spyOn(apiClient.auth, 'listBackupCodes').mockResolvedValue({ unused: 4, total: 10, codes: [] })
    setUser({ totp_enabled: true })
    const { findByText } = renderWithI18n(<TOTPSection />)

    expect(await findByText(/4/)).toBeInTheDocument()
  })

  it('disables TOTP and shows a success message', async () => {
    vi.spyOn(apiClient.auth, 'listBackupCodes').mockResolvedValue({ unused: 4, total: 10, codes: [] })
    vi.spyOn(apiClient.auth, 'disableTOTP').mockResolvedValue(undefined as never)
    setUser({ totp_enabled: true })
    const u = userEvent.setup()
    renderWithI18n(<TOTPSection />)

    await u.click(await screen.findByRole('button', { name: /deaktivieren/i }))

    await vi.waitFor(() => expect(apiClient.auth.disableTOTP).toHaveBeenCalled())
    expect(useAuthStore.getState().user?.totp_enabled).toBe(false)
  })

  it('shows a deactivate error when the API call fails', async () => {
    vi.spyOn(apiClient.auth, 'listBackupCodes').mockResolvedValue({ unused: 4, total: 10, codes: [] })
    vi.spyOn(apiClient.auth, 'disableTOTP').mockRejectedValue({ body: { error: 'nope' } })
    setUser({ totp_enabled: true })
    const { findByRole, findByText } = renderWithI18n(<TOTPSection />)
    const u = userEvent.setup()

    await u.click(await findByRole('button', { name: /deaktivieren/i }))

    expect(await findByText('nope')).toBeInTheDocument()
  })

  it('regenerates backup codes when confirmed', async () => {
    vi.spyOn(apiClient.auth, 'listBackupCodes').mockResolvedValue({ unused: 4, total: 10, codes: [] })
    vi.spyOn(apiClient.auth, 'regenerateBackupCodes').mockResolvedValue({
      backup_codes: ['new-code-1'],
    } as never)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    setUser({ totp_enabled: true })
    const u = userEvent.setup()
    const { findByRole, findByText } = renderWithI18n(<TOTPSection />)

    await u.click(await findByRole('button', { name: /erzeugen/i }))

    expect(await findByText('new-code-1')).toBeInTheDocument()
  })

  it('does not regenerate when the confirm dialog is declined', async () => {
    vi.spyOn(apiClient.auth, 'listBackupCodes').mockResolvedValue({ unused: 4, total: 10, codes: [] })
    const regenSpy = vi.spyOn(apiClient.auth, 'regenerateBackupCodes')
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    setUser({ totp_enabled: true })
    const u = userEvent.setup()
    const { findByRole } = renderWithI18n(<TOTPSection />)

    await u.click(await findByRole('button', { name: /erzeugen/i }))

    expect(regenSpy).not.toHaveBeenCalled()
  })

  it('finishing the backup-codes screen returns to idle with a success message', async () => {
    vi.spyOn(apiClient.auth, 'listBackupCodes').mockResolvedValue({ unused: 4, total: 10, codes: [] })
    vi.spyOn(apiClient.auth, 'regenerateBackupCodes').mockResolvedValue({
      backup_codes: ['new-code-1'],
    } as never)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    setUser({ totp_enabled: true })
    const u = userEvent.setup()
    const { findByRole, findByText } = renderWithI18n(<TOTPSection />)

    await u.click(await findByRole('button', { name: /erzeugen/i }))
    await u.click(await findByRole('button', { name: /gesichert/i }))

    expect(await findByText(/gespeichert|saved/i)).toBeInTheDocument()
  })
})
