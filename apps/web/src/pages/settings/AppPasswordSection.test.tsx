import { describe, it, expect, beforeEach, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/renderWithI18n'
import { apiClient } from '../../store/auth'
import { AppPasswordSection } from './AppPasswordSection'

function appPassword(over: Record<string, unknown> = {}) {
  return {
    id: 'a1', name: 'Laptop', created_at: '2026-01-01T00:00:00Z',
    last_used_at: null, ...over,
  }
}

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('AppPasswordSection — listing', () => {
  it('shows the empty state when there are none', async () => {
    vi.spyOn(apiClient.auth, 'listAppPasswords').mockResolvedValue({ app_passwords: [] })
    const { findByText } = renderWithI18n(<AppPasswordSection />)
    expect(await findByText('Noch kein Geräte-Passwort angelegt.')).toBeInTheDocument()
  })

  it('renders an entry with its last-used date', async () => {
    vi.spyOn(apiClient.auth, 'listAppPasswords').mockResolvedValue({
      app_passwords: [appPassword({ last_used_at: '2026-02-01T00:00:00Z' })],
    })
    const { findByText } = renderWithI18n(<AppPasswordSection />)
    expect(await findByText('Laptop')).toBeInTheDocument()
    expect(await findByText(/Zuletzt verwendet/)).toBeInTheDocument()
  })
})

describe('AppPasswordSection — creating', () => {
  it('shows the secret once and warns that it will not come back', async () => {
    vi.spyOn(apiClient.auth, 'listAppPasswords').mockResolvedValue({ app_passwords: [] })
    const create = vi.spyOn(apiClient.auth, 'createAppPassword').mockResolvedValue({
      id: 'a1', name: 'Laptop', created_at: '2026-01-01T00:00:00Z',
      secret: 'abcd-efgh-ijkl-mnop',
    })

    const { findByText, getByLabelText, getByRole } = renderWithI18n(<AppPasswordSection />)
    await userEvent.click(await findByText('Geräte-Passwort erzeugen'))
    await userEvent.type(getByLabelText('Für welches Gerät oder Programm?'), 'Laptop')
    await userEvent.click(getByRole('button', { name: 'Erzeugen' }))

    expect(create).toHaveBeenCalledWith('Laptop')
    expect(await findByText('abcd-efgh-ijkl-mnop')).toBeInTheDocument()
    expect(await findByText(/nur ein einziges Mal angezeigt/)).toBeInTheDocument()
  })

  it('does not submit an empty name', async () => {
    vi.spyOn(apiClient.auth, 'listAppPasswords').mockResolvedValue({ app_passwords: [] })
    const create = vi.spyOn(apiClient.auth, 'createAppPassword')

    const { findByText, getByRole } = renderWithI18n(<AppPasswordSection />)
    await userEvent.click(await findByText('Geräte-Passwort erzeugen'))
    expect(getByRole('button', { name: 'Erzeugen' })).toBeDisabled()
    expect(create).not.toHaveBeenCalled()
  })
})

describe('AppPasswordSection — revoking', () => {
  it('revokes after confirmation and reloads the list', async () => {
    const list = vi.spyOn(apiClient.auth, 'listAppPasswords')
      .mockResolvedValue({ app_passwords: [appPassword()] })
    const del = vi.spyOn(apiClient.auth, 'deleteAppPassword').mockResolvedValue(undefined)
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    const { findByRole } = renderWithI18n(<AppPasswordSection />)
    await userEvent.click(await findByRole('button', { name: /Widerrufen/ }))

    expect(del).toHaveBeenCalledWith('a1')
    expect(list).toHaveBeenCalledTimes(2)
  })

  it('keeps the credential when the confirmation is declined', async () => {
    vi.spyOn(apiClient.auth, 'listAppPasswords')
      .mockResolvedValue({ app_passwords: [appPassword()] })
    const del = vi.spyOn(apiClient.auth, 'deleteAppPassword')
    vi.spyOn(window, 'confirm').mockReturnValue(false)

    const { findByRole } = renderWithI18n(<AppPasswordSection />)
    await userEvent.click(await findByRole('button', { name: /Widerrufen/ }))

    expect(del).not.toHaveBeenCalled()
  })
})
