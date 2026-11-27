import { render, type RenderOptions } from '@testing-library/react'
import { setupI18n, changeLanguage } from '@privasync/i18n'
import type { ReactElement } from 'react'

let initialised = false

function ensureI18n(lang: 'de' | 'en') {
  if (!initialised) {
    setupI18n([lang])
    initialised = true
  } else {
    changeLanguage(lang)
  }
}

export function renderWithI18n(
  ui: ReactElement,
  opts: { lang?: 'de' | 'en' } & Omit<RenderOptions, 'wrapper'> = {},
) {
  const { lang = 'de', ...rest } = opts
  ensureI18n(lang)
  return render(ui, rest)
}
