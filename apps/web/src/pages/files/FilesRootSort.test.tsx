import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { Routes, Route } from 'react-router-dom'
import { renderWithI18n } from '../../test/renderWithI18n'
import FilesPage from '../FilesPage'
import { apiClient, useAuthStore } from '../../store/auth'
import type { SyncRoot } from '@privasync/api-client'

vi.mock('../../hooks/useSSE', () => ({ useSSE: () => {} }))

const alpha: SyncRoot = {
  id: 'r-a', user_id: 'u', name: 'Alpha', path: '/Alpha',
  created_at: new Date().toISOString(),
}
const beta: SyncRoot = {
  id: 'r-b', user_id: 'u', name: 'Beta', path: '/Beta',
  created_at: new Date().toISOString(),
}

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  useAuthStore.setState({ user: null, accessToken: null, isAuthenticated: false })
  vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([alpha, beta])
  vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [], next_cursor: '' })
  window.localStorage.clear()
})

function render(route = '/files', lang: 'de' | 'en' = 'de') {
  return renderWithI18n(
    <Routes>
      <Route path="/files/*" element={<FilesPage />} />
    </Routes>,
    { lang, route },
  )
}

// Reads the displayed root rows in order.
function rootNames(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll('[data-entry-key]'))
    .map((el) => el.querySelector('.truncate')?.textContent?.trim() ?? '')
}

describe('FilesPage — Sortieren auf Root-Ebene', () => {
  it('zeigt den "Sortieren nach"-Picker auch auf Root-Ebene (/files)', async () => {
    const { findByText, findByRole } = render('/files')
    await findByText('Alpha')
    expect(await findByRole('button', { name: 'Sortieren nach' })).toBeInTheDocument()
  })

  it('Root-Liste respektiert den geteilten "files"-Sort-Key (Name ↓ dreht um)', async () => {
    window.localStorage.setItem(
      'priva-sync:tableSort:files',
      JSON.stringify({ key: 'name', dir: 'desc' }),
    )
    const { findByText, container } = render('/files')
    await findByText('Alpha')
    expect(rootNames(container)).toEqual(['Beta', 'Alpha'])
  })

  it('Sortier-Auswahl auf Root-Ebene persistiert unter dem "files"-Key und dreht die Liste', async () => {
    const { findByText, findByRole, container } = render('/files')
    await findByText('Alpha')
    expect(rootNames(container)).toEqual(['Alpha', 'Beta'])

    act(() => {
      (document.querySelector('button[aria-label="Sortieren nach"]') as HTMLButtonElement).click()
    })
    const descOpt = await findByRole('menuitem', { name: /Name ↓/ })
    act(() => { descOpt.click() })

    expect(rootNames(container)).toEqual(['Beta', 'Alpha'])
    expect(window.localStorage.getItem('priva-sync:tableSort:files'))
      .toBe(JSON.stringify({ key: 'name', dir: 'desc' }))
  })
})
