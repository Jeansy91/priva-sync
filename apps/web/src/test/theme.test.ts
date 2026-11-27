import { describe, it, expect, beforeEach } from 'vitest'
import { setThemePreference, getThemePreference, initTheme } from '@privasync/ui'
import { stubMatchMedia } from './stubMatchMedia'

describe('theme', () => {
  beforeEach(() => {
    document.documentElement.classList.remove('dark')
    localStorage.clear()
    stubMatchMedia(false)
  })

  it('setThemePreference("dark") adds the dark class on <html>', () => {
    setThemePreference('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('setThemePreference("light") removes the dark class', () => {
    setThemePreference('dark')
    setThemePreference('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('persists the preference and getThemePreference returns it', () => {
    setThemePreference('dark')
    expect(getThemePreference()).toBe('dark')
  })

  it('defaults to "system" when nothing is stored', () => {
    expect(getThemePreference()).toBe('system')
  })

  it('initTheme applies the stored preference', () => {
    setThemePreference('dark')
    document.documentElement.classList.remove('dark')
    initTheme()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('"system" follows a dark OS preference', () => {
    stubMatchMedia(true)
    initTheme()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('"system" follows OS preference changes live', () => {
    const media = stubMatchMedia(false)
    initTheme()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    media.setSystemDark(true)
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('an explicit preference ignores OS changes', () => {
    const media = stubMatchMedia(false)
    initTheme()
    setThemePreference('light')
    media.setSystemDark(true)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})
