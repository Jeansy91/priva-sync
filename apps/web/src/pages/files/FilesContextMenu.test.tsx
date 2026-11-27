import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { Routes, Route } from 'react-router-dom'
import { fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import FilesPage from '../FilesPage'
import { apiClient, useAuthStore } from '../../store/auth'
import type { SyncRoot, FileEntry } from '@privasync/api-client'

vi.mock('../../hooks/useSSE', () => ({ useSSE: () => {} }))

const root: SyncRoot = {
  id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
  created_at: new Date().toISOString(),
}
const file: FileEntry = {
  id: 'f-1', path: '/hello.txt', size: 5,
  mtime: new Date().toISOString(), content_hash: '',
  is_deleted: false, updated_at: new Date().toISOString(),
}
const subFile: FileEntry = {
  id: 'f-2', path: '/sub/inner.txt', size: 3,
  mtime: new Date().toISOString(), content_hash: '',
  is_deleted: false, updated_at: new Date().toISOString(),
}

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  useAuthStore.setState({ user: null, accessToken: null, isAuthenticated: false })
  vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
  vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [file], next_cursor: '' })
  window.localStorage.clear()
})

function render(route = '/files/Photos') {
  return renderWithI18n(
    <Routes>
      <Route path="/files/*" element={<FilesPage />} />
    </Routes>,
    { lang: 'de', route },
  )
}

describe('FilesPage — Kontextmenü (Rechtsklick)', () => {
  it('Rechtsklick auf Datei-Zeile oeffnet App-Menue und unterdrueckt Browser-Default', async () => {
    const { findByText, queryByRole, getAllByRole } = render()
    const row = (await findByText('hello.txt')).closest('tr')!
    const evt = new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 50, clientY: 60 })
    act(() => { row.dispatchEvent(evt) })
    expect(evt.defaultPrevented).toBe(true)
    const menuitems = getAllByRole('menuitem')
    const labels = menuitems.map((m) => m.textContent)
    expect(labels.some((l) => l?.includes('Herunterladen'))).toBe(true)
    expect(labels.some((l) => l?.includes('Teilen'))).toBe(true)
    expect(labels.some((l) => l?.includes('Löschen'))).toBe(true)
    // Esc schliesst
    act(() => { fireEvent.keyDown(window, { key: 'Escape' }) })
    expect(queryByRole('menu')).toBeNull()
  })

  it('Rechtsklick auf leere Flaeche oeffnet Menue fuer aktuelles Verzeichnis', async () => {
    vi.mocked(apiClient.files.list).mockResolvedValue({ files: [subFile], next_cursor: '' })
    const { findByText, getAllByRole, container } = render('/files/Photos/sub')
    await findByText('inner.txt')
    const wrapper = container.querySelector('[data-files-content]') as HTMLElement
    expect(wrapper).toBeTruthy()
    const evt = new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 200, clientY: 300 })
    act(() => { wrapper.dispatchEvent(evt) })
    expect(evt.defaultPrevented).toBe(true)
    const labels = getAllByRole('menuitem').map((m) => m.textContent ?? '')
    expect(labels.some((l) => l.includes('Als ZIP herunterladen'))).toBe(true)
    expect(labels.some((l) => l.includes('Neuer Ordner'))).toBe(true)
    expect(labels.some((l) => l.includes('Hochladen'))).toBe(true)
    expect(labels.some((l) => l.includes('Teilen'))).toBe(true)
    expect(labels.some((l) => l.includes('Öffnen'))).toBe(false)
    expect(labels.some((l) => l.includes('Löschen'))).toBe(false)
  })

  it('Rechtsklick auf leere Flaeche an Sync-Root-Ebene: kein Loeschen (zu gefaehrlich), Neuer Ordner + Upload vorhanden', async () => {
    const { findByText, getAllByRole, container } = render('/files/Photos')
    await findByText('hello.txt')
    const wrapper = container.querySelector('[data-files-content]') as HTMLElement
    const evt = new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 200, clientY: 300 })
    act(() => { wrapper.dispatchEvent(evt) })
    const labels = getAllByRole('menuitem').map((m) => m.textContent ?? '')
    expect(labels.some((l) => l.includes('Als ZIP herunterladen'))).toBe(true)
    expect(labels.some((l) => l.includes('Neuer Ordner'))).toBe(true)
    expect(labels.some((l) => l.includes('Hochladen'))).toBe(true)
    expect(labels.some((l) => l.includes('Teilen'))).toBe(true)
    expect(labels.some((l) => l.includes('Löschen'))).toBe(false)
  })

  it('Rechtsklick auf SyncRoot-Listenzeile (/files) oeffnet Root-Aktionen', async () => {
    const { findByText, getAllByRole, findByRole } = render('/files')
    const row = (await findByText('Photos')).closest('tr')!
    const evt = new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 100 })
    act(() => { row.dispatchEvent(evt) })
    expect(evt.defaultPrevented).toBe(true)
    const labels = getAllByRole('menuitem').map((m) => m.textContent)
    expect(labels.some((l) => l?.includes('Als ZIP herunterladen'))).toBe(true)
    expect(labels.some((l) => l?.includes('Teilen'))).toBe(true)
    expect(labels.some((l) => l?.includes('Löschen'))).toBe(true)
    const delBtn = getAllByRole('menuitem').find((m) => m.textContent?.includes('Löschen'))!
    act(() => { delBtn.click() })
    expect(await findByRole('dialog')).toHaveTextContent(/Sync-Ordner löschen/i)
  })

  it('Menue-Reihenfolge auf Freiflaeche: Download -> NewFolder -> Upload -> Teilen', async () => {
    vi.mocked(apiClient.files.list).mockResolvedValue({ files: [subFile], next_cursor: '' })
    const { findByText, getAllByRole, container } = render('/files/Photos/sub')
    await findByText('inner.txt')
    const wrapper = container.querySelector('[data-files-content]') as HTMLElement
    act(() => {
      wrapper.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }))
    })
    const labels = getAllByRole('menuitem').map((m) => m.textContent ?? '')
    const idxDl    = labels.findIndex((l) => l.includes('Als ZIP herunterladen'))
    const idxNew   = labels.findIndex((l) => l.includes('Neuer Ordner'))
    const idxUp    = labels.findIndex((l) => l.includes('Hochladen'))
    const idxShare = labels.findIndex((l) => l.includes('Teilen'))
    expect(idxDl).toBeGreaterThan(-1)
    expect(idxDl).toBeLessThan(idxNew)
    expect(idxNew).toBeLessThan(idxUp)
    expect(idxUp).toBeLessThan(idxShare)
  })

  it('Shift+F10 auf fokussierter Zeile oeffnet dasselbe Menue', async () => {
    const { findByText, getAllByRole } = render()
    const row = (await findByText('hello.txt')).closest('tr')!
    act(() => { fireEvent.keyDown(row, { key: 'F10', shiftKey: true }) })
    const menuitems = getAllByRole('menuitem')
    expect(menuitems.length).toBeGreaterThan(0)
  })
})
