import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderWithI18n } from '../test/renderWithI18n'
import SetupPage from './SetupPage'
import { apiClient } from '../store/auth'

beforeEach(() => {
  vi.spyOn(apiClient.setup, 'status').mockResolvedValue({ needs_setup: true } as any)
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

describe('SetupPage — DE', () => {
  it('rendert Header und Token-Feld', async () => {
    const { findByRole, findByLabelText } = renderWithI18n(<SetupPage />, { lang: 'de' })
    expect(await findByRole('heading', { level: 1, name: 'Erstes Setup' })).toBeInTheDocument()
    expect(await findByLabelText('Setup-Token')).toBeInTheDocument()
  })
})

describe('SetupPage — EN', () => {
  it('renders header and token field', async () => {
    const { findByRole, findByLabelText } = renderWithI18n(<SetupPage />, { lang: 'en' })
    expect(await findByRole('heading', { level: 1, name: 'Initial setup' })).toBeInTheDocument()
    expect(await findByLabelText('Setup token')).toBeInTheDocument()
  })
})
