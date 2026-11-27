import { describe, it, expect, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/renderWithI18n'
import TotpForm from './TotpForm'

describe('TotpForm', () => {
  it('submits a pasted 6-digit code', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    const { getByLabelText } = renderWithI18n(
      <TotpForm onSubmit={onSubmit} onUseBackup={vi.fn()} onBack={vi.fn()} apiError={null} />,
    )
    const user = userEvent.setup()

    const firstDigit = getByLabelText('Authenticator-Code 1')
    await user.click(firstDigit)
    await user.paste('123456')

    expect(onSubmit.mock.calls[0][0]).toEqual({ code: '123456' })
  })

  it('calls onUseBackup and onBack', async () => {
    const onUseBackup = vi.fn()
    const onBack = vi.fn()
    const { getByRole } = renderWithI18n(
      <TotpForm onSubmit={vi.fn()} onUseBackup={onUseBackup} onBack={onBack} apiError={null} />,
    )
    const user = userEvent.setup()

    await user.click(getByRole('button', { name: /backup-code verwenden/i }))
    expect(onUseBackup).toHaveBeenCalled()

    await user.click(getByRole('button', { name: /zurueck zur anmeldung|zurück zur anmeldung/i }))
    expect(onBack).toHaveBeenCalled()
  })

  it('renders an API error message when provided', () => {
    const { getByText } = renderWithI18n(
      <TotpForm onSubmit={vi.fn()} onUseBackup={vi.fn()} onBack={vi.fn()} apiError="Falscher Code" />,
    )
    expect(getByText('Falscher Code')).toBeInTheDocument()
  })
})
