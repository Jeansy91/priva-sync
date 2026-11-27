import { describe, it, expect } from 'vitest'
import { bootLanguageCandidates } from './bootLanguage'

describe('bootLanguageCandidates (web)', () => {
  it('puts the stored localStorage value first', () => {
    expect(bootLanguageCandidates('de', ['en-US', 'en'], 'en-US')[0]).toBe('de')
  })

  it('falls back to navigator list when nothing stored', () => {
    expect(bootLanguageCandidates(null, ['en-US', 'en'], 'en-US'))
      .toEqual(['en-US', 'en'])
  })

  it('ignores empty / whitespace stored values', () => {
    expect(bootLanguageCandidates('  ', ['de-DE'], 'de-DE'))
      .toEqual(['de-DE'])
  })

  it('uses navigator.language when languages list is empty', () => {
    expect(bootLanguageCandidates(null, [], 'en-US')).toEqual(['en-US'])
  })
})
