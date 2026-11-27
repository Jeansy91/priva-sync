import { describe, it, expect, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/renderWithI18n'
import LoginForm from './LoginForm'

describe('LoginForm', () => {
  it('submits username and password', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    const { getByLabelText, getByRole } = renderWithI18n(
      <LoginForm onSubmit={onSubmit} apiError={null} />,
    )
    const user = userEvent.setup()

    await user.type(getByLabelText(/benutzername/i), 'alice')
    await user.type(getByLabelText(/passwort/i), 'hunter2')
    await user.click(getByRole('button', { name: /anmelden/i }))

    expect(onSubmit.mock.calls[0][0]).toEqual({ username: 'alice', password: 'hunter2' })
  })

  it('rejects empty submission with validation errors', async () => {
    const onSubmit = vi.fn()
    const { getByRole, findByText } = renderWithI18n(
      <LoginForm onSubmit={onSubmit} apiError={null} />,
    )
    const user = userEvent.setup()

    await user.click(getByRole('button', { name: /anmelden/i }))

    expect(await findByText('Benutzername erforderlich')).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('renders an API error message when provided', () => {
    const { getByText } = renderWithI18n(
      <LoginForm onSubmit={vi.fn()} apiError="Ungueltige Anmeldedaten" />,
    )
    expect(getByText('Ungueltige Anmeldedaten')).toBeInTheDocument()
  })
})
