import { describe, it, expect } from 'vitest'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { render } from '@testing-library/react'
import { setupI18n, changeLanguage } from '@privasync/i18n'
import { AdminLayout } from './AdminLayout'

let i18nReady = false
function ensureI18n() {
  if (!i18nReady) { setupI18n(['de']); i18nReady = true }
  changeLanguage('de')
}

describe('AdminLayout', () => {
  it('rendert Outlet ohne eigene Navigation', () => {
    ensureI18n()
    const { getByText, queryByRole } = render(
      <MemoryRouter initialEntries={['/admin/users']}>
        <Routes>
          <Route path="/admin" element={<AdminLayout />}>
            <Route path="users" element={<div>Inhalt</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    )
    expect(getByText('Inhalt')).toBeInTheDocument()
    expect(queryByRole('link', { name: 'Audit-Log' })).toBeNull()
  })
})
