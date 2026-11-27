import { describe, it, expect, beforeEach, vi } from 'vitest'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { render } from '@testing-library/react'
import { setupI18n, changeLanguage } from '@privasync/i18n'
import { AdminRoutes } from './AdminRoutes'
import { apiClient } from '../../store/auth'

let i18nReady = false
function ensureI18n() {
  if (!i18nReady) { setupI18n(['de']); i18nReady = true }
  changeLanguage('de')
}

beforeEach(() => {
  vi.spyOn(apiClient.admin, 'listUsers').mockResolvedValue([])
  vi.spyOn(apiClient.admin, 'listAuditLog' as any).mockResolvedValue({ events: [], count: 0 })
  vi.spyOn(console, 'error').mockImplementation(() => {})
  ensureI18n()
})

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/admin/*" element={<AdminRoutes />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('AdminRoutes', () => {
  it('/admin leitet auf /admin/users weiter (Benutzerverwaltung-Header sichtbar)', async () => {
    const { findByRole } = renderAt('/admin')
    expect(
      await findByRole('heading', { level: 1, name: 'Benutzerverwaltung' }, { timeout: 5000 }),
    ).toBeInTheDocument()
  })

  it('/admin/audit-log rendert Audit-Log-Seite', async () => {
    const { findByRole } = renderAt('/admin/audit-log')
    expect(
      await findByRole('heading', { level: 1, name: 'Audit-Log' }, { timeout: 5000 }),
    ).toBeInTheDocument()
  })
})
