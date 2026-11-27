import { describe, it, expect, beforeEach, vi } from 'vitest'
import { Route, Routes } from 'react-router-dom'
import { renderWithI18n } from '../test/renderWithI18n'
import PublicSharePage from './PublicSharePage'
import { apiClient } from '../store/auth'

beforeEach(() => {
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

function renderAt(path: string) {
  return renderWithI18n(
    <Routes>
      <Route path="/s/:token" element={<PublicSharePage />} />
    </Routes>,
    { lang: 'de', route: path },
  )
}

describe('PublicSharePage', () => {
  it('listet Verzeichnis-Eintraege fuer Public-Share', async () => {
    vi.spyOn(apiClient.shares, 'publicEntries').mockResolvedValue({
      share_type: 'public',
      path: '/folder',
      is_file: false,
      can_write: false,
      entries: [
        { name: 'a.txt', path: '/folder/a.txt', is_dir: false, size: 5, mtime: new Date().toISOString() },
        { name: 'sub',   path: '/folder/sub',   is_dir: true,  size: 0, mtime: new Date().toISOString() },
      ],
    })

    const { findByText } = renderAt('/s/tok-1')
    expect(await findByText('a.txt')).toBeInTheDocument()
    expect(await findByText('sub')).toBeInTheDocument()
  })

  it('sortiert Ordner vor Dateien, jeweils alphabetisch', async () => {
    vi.spyOn(apiClient.shares, 'publicEntries').mockResolvedValue({
      share_type: 'public',
      path: '/folder',
      is_file: false,
      can_write: false,
      entries: [
        { name: 'b.txt', path: '/folder/b.txt', is_dir: false, size: 1, mtime: new Date().toISOString() },
        { name: 'zeta',  path: '/folder/zeta',  is_dir: true,  size: 0, mtime: new Date().toISOString() },
        { name: 'a.txt', path: '/folder/a.txt', is_dir: false, size: 1, mtime: new Date().toISOString() },
        { name: 'Alpha', path: '/folder/Alpha', is_dir: true,  size: 0, mtime: new Date().toISOString() },
      ],
    })

    const { findAllByRole } = renderAt('/s/tok-sort')
    const items = await findAllByRole('listitem')
    const names = items.map((li) => li.textContent ?? '')
    expect(names[0]).toContain('Alpha')
    expect(names[1]).toContain('zeta')
    expect(names[2]).toContain('a.txt')
    expect(names[3]).toContain('b.txt')
  })

  it('zeigt Alles-Herunterladen und Ordner-Download fuer Dir-Share', async () => {
    vi.spyOn(apiClient.shares, 'publicEntries').mockResolvedValue({
      share_type: 'public',
      path: '/folder',
      is_file: false,
      can_write: false,
      entries: [
        { name: 'sub', path: '/folder/sub', is_dir: true, size: 0, mtime: new Date().toISOString() },
      ],
    })

    const { findByRole, findAllByRole } = renderAt('/s/tok-3')

    const all = await findByRole('link', { name: /Alles herunterladen/i })
    expect(all.getAttribute('href')).toMatch(/\/api\/v1\/public\/shares\/tok-3\/download$/)

    const folderDl = (await findAllByRole('link', { name: /Ordner herunterladen/i }))[0]
    expect(folderDl.getAttribute('href')).toContain('sub=sub')
  })

  it('zeigt Download-Button fuer Datei-Share', async () => {
    vi.spyOn(apiClient.shares, 'publicEntries').mockResolvedValue({
      share_type: 'public',
      path: '/doc.txt',
      is_file: true,
      can_write: false,
      entries: [
        { name: 'doc.txt', path: '/doc.txt', is_dir: false, size: 12, mtime: new Date().toISOString() },
      ],
    })

    const { findByRole } = renderAt('/s/tok-2')
    const link = await findByRole('link', { name: /Herunterladen/i })
    expect(link).toBeInTheDocument()
    expect(link.getAttribute('href')).toContain('/api/v1/public/shares/tok-2/download')
  })
})
