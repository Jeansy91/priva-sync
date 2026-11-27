import { describe, it, expect, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/renderWithI18n'
import BackupForm from './BackupForm'

describe('BackupForm', () => {
  it('submits the entered backup code', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    const { getByLabelText, getByRole } = renderWithI18n(
      <BackupForm onSubmit={onSubmit} onUseAuthenticator={vi.fn()} onBack={vi.fn()} apiError={null} />,
    )
    const user = userEvent.setup()

    await user.type(getByLabelText('Backup-Code'), 'abcd-ef12-3456-7890')
    await user.click(getByRole('button', { name: /code einlösen|code einloesen/i }))

    expect(onSubmit.mock.calls[0][0]).toEqual({ code: 'abcd-ef12-3456-7890' })
  })

  it('rejects empty submission', async () => {
    const onSubmit = vi.fn()
    const { getByRole, findByText } = renderWithI18n(
      <BackupForm onSubmit={onSubmit} onUseAuthenticator={vi.fn()} onBack={vi.fn()} apiError={null} />,
    )
    const user = userEvent.setup()

    await user.click(getByRole('button', { name: /code einlösen|code einloesen/i }))

    expect(await findByText('Backup-Code erforderlich')).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('calls onUseAuthenticator and onBack', async () => {
    const onUseAuthenticator = vi.fn()
    const onBack = vi.fn()
    const { getByRole } = renderWithI18n(
      <BackupForm onSubmit={vi.fn()} onUseAuthenticator={onUseAuthenticator} onBack={onBack} apiError={null} />,
    )
    const user = userEvent.setup()

    await user.click(getByRole('button', { name: /authenticator-code verwenden/i }))
    expect(onUseAuthenticator).toHaveBeenCalled()

    await user.click(getByRole('button', { name: /zurück zur anmeldung|zurueck zur anmeldung/i }))
    expect(onBack).toHaveBeenCalled()
  })
})
