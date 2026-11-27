import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { Routes, Route, useLocation } from 'react-router-dom'
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
const nestedFile: FileEntry = {
  id: 'f-2', path: '/sub/in.txt', size: 3,
  mtime: new Date().toISOString(), content_hash: '',
  is_deleted: false, updated_at: new Date().toISOString(),
}

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  useAuthStore.setState({ user: null, accessToken: null, isAuthenticated: false })
  vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
  vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [file, nestedFile], next_cursor: '' })
  window.localStorage.clear()
})

function LocationProbe({ onChange }: { onChange: (p: string) => void }) {
  const loc = useLocation()
  onChange(loc.pathname)
  return null
}

function render(route = '/files/Photos', lang: 'de' | 'en' = 'de') {
  let lastPath = route
  const utils = renderWithI18n(
    <>
      <LocationProbe onChange={(p) => { lastPath = p }} />
      <Routes>
        <Route path="/files/*" element={<FilesPage />} />
      </Routes>
    </>,
    { lang, route },
  )
  return { ...utils, getPath: () => lastPath }
}

describe('FilesPage — Ansichten-Switcher', () => {
  it('rendert Tabelle/Raster-Buttons, Tabelle ist Default (aria-pressed=true)', async () => {
    const { findByRole } = render()
    const tableBtn = await findByRole('button', { name: 'Tabelle' })
    const gridBtn  = await findByRole('button', { name: 'Raster' })
    expect(tableBtn).toHaveAttribute('aria-pressed', 'true')
    expect(gridBtn).toHaveAttribute('aria-pressed', 'false')
  })

  it('rendert Tabelle (role=table) wenn Mode=Tabelle', async () => {
    const { findByText, getByRole } = render()
    await findByText('hello.txt')
    expect(getByRole('table')).toBeInTheDocument()
  })

  it('Klick auf Raster blendet Tabelle aus und zeigt Grid', async () => {
    const { findByText, findByRole, queryByRole, getByTestId } = render()
    await findByText('hello.txt')
    const gridBtn = await findByRole('button', { name: 'Raster' })
    act(() => { gridBtn.click() })
    expect(gridBtn).toHaveAttribute('aria-pressed', 'true')
    expect(queryByRole('table')).not.toBeInTheDocument()
    expect(getByTestId('files-grid')).toBeInTheDocument()
  })

  it('Doubleclick auf Ordner-Tile navigiert in den Ordner', async () => {
    const { findByText, findByRole, getByTestId, getPath } = render()
    await findByText('hello.txt')
    const gridBtn = await findByRole('button', { name: 'Raster' })
    act(() => { gridBtn.click() })
    const grid = getByTestId('files-grid')
    const tiles = grid.querySelectorAll('button')
    const subTile = Array.from(tiles).find((b) => b.textContent?.includes('sub'))
    expect(subTile).toBeDefined()
    act(() => { fireEvent.doubleClick(subTile!) })
    expect(getPath()).toBe('/files/Photos/sub')
  })

  it('liest View-Mode aus localStorage (priva.filesViewMode=grid)', async () => {
    window.localStorage.setItem('privasync.filesViewMode', 'grid')
    const { findByRole, findByText, getByTestId, queryByRole } = render()
    await findByText('hello.txt')
    const gridBtn = await findByRole('button', { name: 'Raster' })
    expect(gridBtn).toHaveAttribute('aria-pressed', 'true')
    expect(getByTestId('files-grid')).toBeInTheDocument()
    expect(queryByRole('table')).not.toBeInTheDocument()
  })

  it('persistiert View-Mode in localStorage beim Klick auf Raster', async () => {
    const { findByText, findByRole } = render()
    await findByText('hello.txt')
    const gridBtn = await findByRole('button', { name: 'Raster' })
    act(() => { gridBtn.click() })
    expect(window.localStorage.getItem('privasync.filesViewMode')).toBe('grid')
  })

  it('zeigt Sortier-Icon-Button in der Toolbar', async () => {
    const { findByText, findByRole } = render()
    await findByText('hello.txt')
    expect(await findByRole('button', { name: 'Sortieren nach' })).toBeInTheDocument()
  })

  it('Sortier-Menü-Auswahl "Name ↓" dreht die Reihenfolge in beiden Views', async () => {
    const apple: FileEntry = {
      id: 'fa', path: '/apple.txt', size: 1,
      mtime: new Date().toISOString(), content_hash: '',
      is_deleted: false, updated_at: new Date().toISOString(),
    }
    const banana: FileEntry = {
      id: 'fb', path: '/banana.txt', size: 1,
      mtime: new Date().toISOString(), content_hash: '',
      is_deleted: false, updated_at: new Date().toISOString(),
    }
    vi.mocked(apiClient.files.list).mockResolvedValue({ files: [apple, banana], next_cursor: '' })
    window.localStorage.setItem('privasync.filesViewMode', 'grid')
    const { findByRole, findByText, getByTestId } = render()
    await findByText('apple.txt')

    // Icon-Button öffnet das Menü.
    act(() => { (document.querySelector('button[aria-label="Sortieren nach"]') as HTMLButtonElement).click() })
    const descOpt = await findByRole('menuitem', { name: /Name ↓/ })
    act(() => { descOpt.click() })

    const names = Array.from(getByTestId('files-grid').querySelectorAll('button'))
      .map((b) => b.textContent?.trim() ?? '')
    expect(names).toEqual(['banana.txt', 'apple.txt'])
  })

  it('Grid-Tiles nutzen Namen-Wrap statt truncate', async () => {
    const longName: FileEntry = {
      id: 'fl', path: '/ein-sehr-langer-dateiname-der-umbrechen-soll.txt', size: 1,
      mtime: new Date().toISOString(), content_hash: '',
      is_deleted: false, updated_at: new Date().toISOString(),
    }
    vi.mocked(apiClient.files.list).mockResolvedValue({ files: [longName], next_cursor: '' })
    window.localStorage.setItem('privasync.filesViewMode', 'grid')
    const { findByTitle, getByTestId } = render()
    const label = await findByTitle('ein-sehr-langer-dateiname-der-umbrechen-soll.txt')
    expect(label.className).toMatch(/wrap-break-word/)
    expect(label.className).not.toMatch(/\btruncate\b/)
    expect(label.className).not.toMatch(/line-clamp/)
    // Tile-Container muss zum Grid gehoeren.
    expect(getByTestId('files-grid').contains(label)).toBe(true)
  })

  it('filesSort wirkt auch im Grid (desc dreht Reihenfolge)', async () => {
    const apple: FileEntry = {
      id: 'fa', path: '/apple.txt', size: 1,
      mtime: new Date().toISOString(), content_hash: '',
      is_deleted: false, updated_at: new Date().toISOString(),
    }
    const banana: FileEntry = {
      id: 'fb', path: '/banana.txt', size: 1,
      mtime: new Date().toISOString(), content_hash: '',
      is_deleted: false, updated_at: new Date().toISOString(),
    }
    vi.mocked(apiClient.files.list).mockResolvedValue({ files: [apple, banana], next_cursor: '' })
    window.localStorage.setItem(
      'priva-sync:tableSort:files',
      JSON.stringify({ key: 'name', dir: 'desc' }),
    )
    window.localStorage.setItem('privasync.filesViewMode', 'grid')
    const { findByText, getByTestId } = render()
    await findByText('apple.txt')
    const grid = getByTestId('files-grid')
    const tiles = Array.from(grid.querySelectorAll('button'))
    const names = tiles.map((b) => b.textContent?.trim() ?? '')
    expect(names).toEqual(['banana.txt', 'apple.txt'])
  })

  it('Selektion bleibt beim View-Wechsel erhalten', async () => {
    const { findByText, findByRole, getByTestId } = render()
    const row = await findByText('hello.txt')
    act(() => { fireEvent.click(row) })
    const gridBtn = await findByRole('button', { name: 'Raster' })
    act(() => { gridBtn.click() })
    const grid = getByTestId('files-grid')
    const tiles = Array.from(grid.querySelectorAll('button'))
    const fileTile = tiles.find((b) => b.textContent?.includes('hello.txt'))
    expect(fileTile).toBeDefined()
    expect(fileTile!.className).toMatch(/bg-accent\/40/)
  })

  it('Grid zeigt Dateinamen und Ordnernamen als Tiles', async () => {
    const { findByText, findByRole, getByTestId } = render()
    await findByText('hello.txt')
    const gridBtn = await findByRole('button', { name: 'Raster' })
    act(() => { gridBtn.click() })
    const grid = getByTestId('files-grid')
    expect(grid).toHaveTextContent('hello.txt')
    expect(grid).toHaveTextContent('sub')
  })
})
