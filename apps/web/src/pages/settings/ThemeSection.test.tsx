import { describe, it, expect, beforeEach } from 'vitest'
import { screen, fireEvent } from '@testing-library/react'
import { getThemePreference } from '@privasync/ui'
import { renderWithI18n } from '../../test/renderWithI18n'
import { stubMatchMedia } from '../../test/stubMatchMedia'
import { ThemeSection } from './ThemeSection'

describe('ThemeSection', () => {
  beforeEach(() => {
    document.documentElement.classList.remove('dark')
    localStorage.clear()
    stubMatchMedia(false)
  })

  it('shows "System" as the default choice', () => {
    renderWithI18n(<ThemeSection />)
    expect(screen.getByRole('button', { name: /System/ })).toBeInTheDocument()
  })

  it('selecting "Dunkel" applies and persists the dark theme', () => {
    renderWithI18n(<ThemeSection />)
    fireEvent.click(screen.getByRole('button', { name: /System/ }))
    fireEvent.click(screen.getByRole('button', { name: /Dunkel/ }))
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(getThemePreference()).toBe('dark')
  })
})
