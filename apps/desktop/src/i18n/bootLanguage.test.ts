import { describe, it, expect } from 'vitest'
import { bootLanguageCandidates } from './bootLanguage'

describe('bootLanguageCandidates', () => {
  it('puts the persisted language first when present', () => {
    const out = bootLanguageCandidates('de', ['en-US', 'en'], 'en-US')
    expect(out[0]).toBe('de')
  })

  it('falls back to navigator list when no DB pref', () => {
    expect(bootLanguageCandidates(null, ['en-US', 'en'], 'en-US'))
      .toEqual(['en-US', 'en'])
  })

  it('ignores empty / whitespace DB values', () => {
    expect(bootLanguageCandidates('   ', ['de-DE'], 'de-DE'))
      .toEqual(['de-DE'])
  })

  it('falls through to navigator.language when navigator.languages missing', () => {
    expect(bootLanguageCandidates(null, [], 'en-US'))
      .toEqual(['en-US'])
  })

  it('returns at least an empty array, never undefined', () => {
    expect(bootLanguageCandidates(null, [], '')).toEqual([])
  })
})
