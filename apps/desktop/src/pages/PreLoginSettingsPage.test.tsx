import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../test/renderWithI18n'

// SettingsTab is exercised on its own in components/SettingsTab.test.tsx —
// here it only stands in for "the same settings, no session required".
vi.mock('../components/SettingsTab', () => ({
  default: () => <div>settings-tab</div>,
}))

import PreLoginSettingsPage from './PreLoginSettingsPage'

const onBack = vi.fn()

beforeEach(() => {
  onBack.mockClear()
})

describe('PreLoginSettingsPage', () => {
  it('renders the full settings tab without a session', () => {
    renderWithI18n(<PreLoginSettingsPage onBack={onBack} />)
    expect(screen.getByText('settings-tab')).toBeInTheDocument()
  })

  it('calls onBack from the back button', () => {
    renderWithI18n(<PreLoginSettingsPage onBack={onBack} />)
    fireEvent.click(screen.getByRole('button', { name: 'Zurück' }))
    expect(onBack).toHaveBeenCalledTimes(1)
  })
})
