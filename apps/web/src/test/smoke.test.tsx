import { describe, it, expect } from 'vitest'
import { useTranslation } from '@privasync/i18n'
import { renderWithI18n } from './renderWithI18n'

function Hello() {
  const { t } = useTranslation()
  return <div>{t('common.save')}</div>
}

describe('test infra', () => {
  it('renders translated string in DE', () => {
    const { getByText } = renderWithI18n(<Hello />, { lang: 'de' })
    expect(getByText('Speichern')).toBeInTheDocument()
  })
  it('renders translated string in EN', () => {
    const { getByText } = renderWithI18n(<Hello />, { lang: 'en' })
    expect(getByText('Save')).toBeInTheDocument()
  })
})
