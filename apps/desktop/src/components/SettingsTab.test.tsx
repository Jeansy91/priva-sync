import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { getThemePreference } from '@privasync/ui'
import { renderWithI18n } from '../test/renderWithI18n'

const cmdMock = vi.hoisted(() => ({
  getUploadConcurrency: vi.fn(),
  getAutostart: vi.fn(),
  setUploadConcurrency: vi.fn(),
  setAutostart: vi.fn(),
  setLanguage: vi.fn(),
  checkForUpdate: vi.fn(),
  installUpdate: vi.fn(),
  getIgnorePatterns: vi.fn(),
  getDefaultIgnorePatterns: vi.fn(),
  validateIgnorePatterns: vi.fn(),
  countExcludedFiles: vi.fn(),
  setIgnorePatterns: vi.fn(),
}))

vi.mock('../tauri', () => ({ cmd: cmdMock }))

import SettingsTab from './SettingsTab'

beforeEach(() => {
  cmdMock.getUploadConcurrency.mockResolvedValue(3)
  cmdMock.getAutostart.mockResolvedValue(false)
  cmdMock.getIgnorePatterns.mockResolvedValue('')
  cmdMock.getDefaultIgnorePatterns.mockResolvedValue('')
  cmdMock.validateIgnorePatterns.mockResolvedValue([])
  cmdMock.countExcludedFiles.mockResolvedValue({ total: 0, sample: [] })
  cmdMock.setIgnorePatterns.mockResolvedValue(undefined)
  document.documentElement.classList.remove('dark')
  localStorage.clear()
  // jsdom has no matchMedia — light system preference is enough here.
  vi.stubGlobal('matchMedia', vi.fn(() => ({
    matches: false,
    media: '(prefers-color-scheme: dark)',
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  })))
})

describe('SettingsTab layout', () => {
  it('groups settings into Synchronisierung / Sprache / System / Über cards', async () => {
    renderWithI18n(<SettingsTab />)

    expect(await screen.findByRole('heading', { name: 'Synchronisierung' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Sprache' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'System' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Über' })).toBeInTheDocument()
  })

  it('shows the version inside the About group, not as a standalone footer', () => {
    renderWithI18n(<SettingsTab />)
    const about = screen.getByText('Über').closest('div.rounded-xl') as HTMLElement
    expect(about).toBeTruthy()
    expect(about.textContent).toContain('PrivaSync v')
  })
})

describe('SettingsTab theme', () => {
  it('has an Erscheinungsbild card with the theme select', async () => {
    renderWithI18n(<SettingsTab />)
    expect(await screen.findByText('Erscheinungsbild')).toBeInTheDocument()
  })

  it('selecting Dunkel applies and persists the dark theme', async () => {
    renderWithI18n(<SettingsTab />)
    fireEvent.click(await screen.findByRole('button', { name: 'System' }))
    fireEvent.click(screen.getByRole('button', { name: 'Dunkel' }))
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(getThemePreference()).toBe('dark')
  })
})

describe('SettingsTab autostart', () => {
  it('toggles autostart on and calls the backend', async () => {
    cmdMock.setAutostart.mockResolvedValue(undefined)
    renderWithI18n(<SettingsTab />)

    const toggle = await screen.findByRole('checkbox')
    fireEvent.click(toggle)

    await waitFor(() => expect(cmdMock.setAutostart).toHaveBeenCalledWith(true))
    expect(toggle).toBeChecked()
  })

  it('reverts the toggle and shows an error when the backend call fails', async () => {
    cmdMock.setAutostart.mockRejectedValue(new Error('denied'))
    renderWithI18n(<SettingsTab />)

    const toggle = await screen.findByRole('checkbox')
    fireEvent.click(toggle)

    await waitFor(() => expect(screen.getByText(/denied/)).toBeInTheDocument())
    expect(toggle).not.toBeChecked()
  })
})

describe('SettingsTab concurrency', () => {
  it('saves the new upload-concurrency value', async () => {
    cmdMock.setUploadConcurrency.mockResolvedValue(undefined)
    renderWithI18n(<SettingsTab />)

    const slider = await screen.findByRole('slider')
    fireEvent.change(slider, { target: { value: '5' } })

    await waitFor(() => expect(cmdMock.setUploadConcurrency).toHaveBeenCalledWith(5))
  })
})

describe('SettingsTab language', () => {
  it('switches language and shows the switching overlay', async () => {
    cmdMock.setLanguage.mockResolvedValue(undefined)
    renderWithI18n(<SettingsTab />)

    fireEvent.click(await screen.findByRole('button', { name: 'Deutsch' }))
    fireEvent.click(screen.getByRole('button', { name: 'English' }))

    await waitFor(() => expect(cmdMock.setLanguage).toHaveBeenCalledWith('en'))
  })
})

describe('SettingsTab update check', () => {
  it('shows up-to-date after a check with no update available', async () => {
    cmdMock.checkForUpdate.mockResolvedValue(null)
    renderWithI18n(<SettingsTab />)

    fireEvent.click(await screen.findByRole('button', { name: 'Nach Updates suchen' }))

    await waitFor(() => expect(screen.getByText('Aktuelle Version installiert')).toBeInTheDocument())
  })

  it('offers install when an update is available, and installs it', async () => {
    cmdMock.checkForUpdate.mockResolvedValue({ version: '1.3.0' })
    cmdMock.installUpdate.mockResolvedValue(undefined)
    renderWithI18n(<SettingsTab />)

    fireEvent.click(await screen.findByRole('button', { name: 'Nach Updates suchen' }))
    const installBtn = await screen.findByRole('button', { name: /1\.3\.0 installieren/ })
    fireEvent.click(installBtn)

    await waitFor(() => expect(cmdMock.installUpdate).toHaveBeenCalled())
  })

  it('shows an error when the update check fails', async () => {
    cmdMock.checkForUpdate.mockRejectedValue(new Error('offline'))
    renderWithI18n(<SettingsTab />)

    fireEvent.click(await screen.findByRole('button', { name: 'Nach Updates suchen' }))

    await waitFor(() => expect(screen.getByText(/offline/)).toBeInTheDocument())
  })
})
