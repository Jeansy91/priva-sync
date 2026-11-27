import { describe, it, expect, beforeEach, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/renderWithI18n'
import { apiClient } from '../../store/auth'
import { PasskeySection } from './PasskeySection'
import { passkeysSupported, createPasskey, PasskeyCancelledError } from '../../lib/webauthn'

vi.mock('../../lib/webauthn', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/webauthn')>()
  return {
    ...actual,
    passkeysSupported: vi.fn(() => true),
    createPasskey: vi.fn(),
  }
})

const supported = vi.mocked(passkeysSupported)
const create = vi.mocked(createPasskey)

function passkey(over: Record<string, unknown> = {}) {
  return {
    id: 'p1', name: 'YubiKey', created_at: '2026-01-01T00:00:00Z',
    last_used_at: null, ...over,
  }
}

beforeEach(() => {
  vi.restoreAllMocks()
  supported.mockReturnValue(true)
})

describe('PasskeySection — listing', () => {
  it('shows the empty state when there are none', async () => {
    vi.spyOn(apiClient.auth, 'listPasskeys').mockResolvedValue([])
    const { findByText } = renderWithI18n(<PasskeySection />)
    expect(await findByText('Noch kein Passkey hinterlegt.')).toBeInTheDocument()
  })

  it('renders a passkey with its last-used date', async () => {
    vi.spyOn(apiClient.auth, 'listPasskeys').mockResolvedValue([
      passkey({ last_used_at: '2026-02-01T00:00:00Z' }),
    ])
    const { findByText } = renderWithI18n(<PasskeySection />)
    expect(await findByText('YubiKey')).toBeInTheDocument()
    expect(await findByText(/Zuletzt verwendet/)).toBeInTheDocument()
  })

  it('renders "never used" when there is no last-used date', async () => {
    vi.spyOn(apiClient.auth, 'listPasskeys').mockResolvedValue([passkey()])
    const { findByText } = renderWithI18n(<PasskeySection />)
    expect(await findByText('Noch nicht verwendet')).toBeInTheDocument()
  })

  it('falls back to an empty list when loading fails', async () => {
    vi.spyOn(apiClient.auth, 'listPasskeys').mockRejectedValue(new Error('down'))
    const { findByText } = renderWithI18n(<PasskeySection />)
    expect(await findByText('Noch kein Passkey hinterlegt.')).toBeInTheDocument()
  })

  it('shows the unsupported hint when the browser lacks WebAuthn', async () => {
    supported.mockReturnValue(false)
    vi.spyOn(apiClient.auth, 'listPasskeys').mockResolvedValue([])
    const { findByText } = renderWithI18n(<PasskeySection />)
    expect(await findByText('Dein Browser unterstützt keine Passkeys.')).toBeInTheDocument()
  })
})

describe('PasskeySection — delete', () => {
  it('removes a passkey when confirmed', async () => {
    vi.spyOn(apiClient.auth, 'listPasskeys').mockResolvedValue([passkey()])
    vi.spyOn(apiClient.auth, 'deletePasskey').mockResolvedValue(undefined as never)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const u = userEvent.setup()
    const { findByRole } = renderWithI18n(<PasskeySection />)

    await u.click(await findByRole('button', { name: /Entfernen/i }))

    await vi.waitFor(() => expect(apiClient.auth.deletePasskey).toHaveBeenCalledWith('p1'))
  })

  it('does not remove when the confirm dialog is declined', async () => {
    vi.spyOn(apiClient.auth, 'listPasskeys').mockResolvedValue([passkey()])
    const deleteSpy = vi.spyOn(apiClient.auth, 'deletePasskey')
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    const u = userEvent.setup()
    const { findByRole } = renderWithI18n(<PasskeySection />)

    await u.click(await findByRole('button', { name: /Entfernen/i }))

    expect(deleteSpy).not.toHaveBeenCalled()
  })

  it('shows an error when deletion fails', async () => {
    vi.spyOn(apiClient.auth, 'listPasskeys').mockResolvedValue([passkey()])
    vi.spyOn(apiClient.auth, 'deletePasskey').mockRejectedValue({ body: { error: 'server error' } })
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const u = userEvent.setup()
    const { findByRole, findByText } = renderWithI18n(<PasskeySection />)

    await u.click(await findByRole('button', { name: /Entfernen/i }))

    expect(await findByText('server error')).toBeInTheDocument()
  })
})

describe('PasskeySection — add', () => {
  it('walks through add -> name -> save', async () => {
    vi.spyOn(apiClient.auth, 'listPasskeys').mockResolvedValue([])
    vi.spyOn(apiClient.auth, 'passkeyRegisterBegin').mockResolvedValue({
      challenge_token: 'tok', options: {},
    } as never)
    create.mockResolvedValue({ id: 'cred' })
    vi.spyOn(apiClient.auth, 'passkeyRegisterFinish').mockResolvedValue(passkey({ id: 'p2' }) as never)
    const u = userEvent.setup()
    const { findByRole, findByLabelText } = renderWithI18n(<PasskeySection />)

    await u.click(await findByRole('button', { name: /Passkey hinzufügen/i }))
    const nameInput = await findByLabelText('Name für diesen Passkey')
    await u.type(nameInput, 'Mein Handy')
    await u.click(await findByRole('button', { name: /Speichern/i }))

    await vi.waitFor(() => expect(apiClient.auth.passkeyRegisterFinish).toHaveBeenCalledWith({
      challenge_token: 'tok', credential: { id: 'cred' }, name: 'Mein Handy',
    }))
  })

  it('uses the default name when left blank', async () => {
    vi.spyOn(apiClient.auth, 'listPasskeys').mockResolvedValue([])
    vi.spyOn(apiClient.auth, 'passkeyRegisterBegin').mockResolvedValue({
      challenge_token: 'tok', options: {},
    } as never)
    create.mockResolvedValue({ id: 'cred' })
    const finishSpy = vi.spyOn(apiClient.auth, 'passkeyRegisterFinish').mockResolvedValue(passkey() as never)
    const u = userEvent.setup()
    const { findByRole } = renderWithI18n(<PasskeySection />)

    await u.click(await findByRole('button', { name: /Passkey hinzufügen/i }))
    await u.click(await findByRole('button', { name: /Speichern/i }))

    await vi.waitFor(() => expect(finishSpy).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'Passkey' }),
    ))
  })

  it('cancels the name step without saving', async () => {
    vi.spyOn(apiClient.auth, 'listPasskeys').mockResolvedValue([])
    vi.spyOn(apiClient.auth, 'passkeyRegisterBegin').mockResolvedValue({
      challenge_token: 'tok', options: {},
    } as never)
    create.mockResolvedValue({ id: 'cred' })
    const u = userEvent.setup()
    const { findByRole } = renderWithI18n(<PasskeySection />)

    await u.click(await findByRole('button', { name: /Passkey hinzufügen/i }))
    await u.click(await findByRole('button', { name: /Abbrechen/i }))

    expect(await findByRole('button', { name: /Passkey hinzufügen/i })).toBeInTheDocument()
  })

  it('stays silent when the ceremony is cancelled by the user', async () => {
    vi.spyOn(apiClient.auth, 'listPasskeys').mockResolvedValue([])
    vi.spyOn(apiClient.auth, 'passkeyRegisterBegin').mockResolvedValue({
      challenge_token: 'tok', options: {},
    } as never)
    create.mockRejectedValue(new PasskeyCancelledError())
    const u = userEvent.setup()
    const { findByRole, queryByText } = renderWithI18n(<PasskeySection />)

    await u.click(await findByRole('button', { name: /Passkey hinzufügen/i }))

    await vi.waitFor(() => expect(create).toHaveBeenCalled())
    expect(queryByText('Fehler beim Hinzufügen des Passkeys')).not.toBeInTheDocument()
  })

  it('shows an error when the registration begin call fails', async () => {
    vi.spyOn(apiClient.auth, 'listPasskeys').mockResolvedValue([])
    vi.spyOn(apiClient.auth, 'passkeyRegisterBegin').mockRejectedValue({ body: { error: 'begin failed' } })
    const u = userEvent.setup()
    const { findByRole, findByText } = renderWithI18n(<PasskeySection />)

    await u.click(await findByRole('button', { name: /Passkey hinzufügen/i }))

    expect(await findByText('begin failed')).toBeInTheDocument()
  })
})
