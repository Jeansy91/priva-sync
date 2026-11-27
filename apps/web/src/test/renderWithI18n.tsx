import { render, type RenderOptions } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { setupI18n, changeLanguage } from '@privasync/i18n'
import type { ReactElement, ReactNode } from 'react'

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
  opts: { lang?: 'de' | 'en'; route?: string } & Omit<RenderOptions, 'wrapper'> = {},
) {
  const { lang = 'de', route = '/', ...rest } = opts
  ensureI18n(lang)
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <MemoryRouter initialEntries={[route]}>{children}</MemoryRouter>
  )
  return render(ui, { wrapper: Wrapper, ...rest })
}
