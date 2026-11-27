import { describe, it, expect, beforeAll } from 'vitest'
import { setupI18n } from '@privasync/i18n'
import { commandErrorMessage } from './commandError'

beforeAll(() => {
  setupI18n(['de'])
})

describe('commandErrorMessage', () => {
  it('translates a coded command error', () => {
    expect(commandErrorMessage({ error: 'invalid credentials', code: 'INVALID_CREDENTIALS' }))
      .toBe('Benutzername oder Passwort ist falsch.')
  })

  it('falls back to the English text when the code is unknown or missing', () => {
    expect(commandErrorMessage({ error: 'disk full', code: 'NO_SUCH_CODE' })).toBe('disk full')
    expect(commandErrorMessage({ error: 'disk full' })).toBe('disk full')
  })

  it('shows a plain string rejection as-is', () => {
    expect(commandErrorMessage('sync root busy')).toBe('sync root busy')
  })
})
