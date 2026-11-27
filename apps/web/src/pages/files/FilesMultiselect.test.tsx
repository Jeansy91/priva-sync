import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { Routes, Route } from 'react-router-dom'
import { fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import FilesPage from '../FilesPage'
import { apiClient, useAuthStore } from '../../store/auth'
import type { SyncRoot, FileEntry } from '@privasync/api-client'

vi.mock('../../hooks/useSSE', () => ({ useSSE: () => {} }))

const root: SyncRoot = {
  id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
  created_at: new Date().toISOString(),
}
function mkFile(name: string, id = name): FileEntry {
  return {
    id, path: '/' + name, size: 1,
    mtime: new Date().toISOString(), content_hash: '',
    is_deleted: false, updated_at: new Date().toISOString(),
  }
}
const files = [mkFile('a.txt'), mkFile('b.txt'), mkFile('c.txt')]

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  useAuthStore.setState({ user: null, accessToken: null, isAuthenticated: false })
  vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
  vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files, next_cursor: '' })
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

describe('FilesPage — Multiselect (Slice 7: Checkbox-Spalte)', () => {
  it('Tabelle rendert Row-Checkboxen + Header-Checkbox', async () => {
    const { findByText, getAllByRole } = render()
    await findByText('a.txt')
    const checkboxes = getAllByRole('checkbox')
    // 3 Rows + 1 Header = 4
    expect(checkboxes.length).toBeGreaterThanOrEqual(4)
  })

  it('Klick auf Row-Checkbox markiert die Zeile (aria-checked=true)', async () => {
    const { findByLabelText } = render()
    const cb = await findByLabelText(/Auswählen.*a\.txt/i)
    act(() => { fireEvent.click(cb) })
    expect(cb).toBeChecked()
  })
})

describe('FilesPage — Multiselect (Bulk-Actions im Detail-Panel)', () => {
  it('Detail-Panel zeigt Anzahl + Bulk-Buttons ab Mehrfach-Auswahl (>1)', async () => {
    const { findByLabelText, queryByTestId, getByTestId } = render()
    expect(queryByTestId('detail-multi')).toBeNull()
    const a = await findByLabelText(/Auswählen.*a\.txt/i)
    const b = await findByLabelText(/Auswählen.*b\.txt/i)
    act(() => { fireEvent.click(a) })
    act(() => { fireEvent.click(b) })
    const panel = getByTestId('detail-multi')
    expect(panel).toHaveTextContent('2 Einträge ausgewählt')
    expect(panel.querySelector('button[type=button]')).not.toBeNull()
  })
})

describe('FilesPage — Multiselect (Detail-Panel)', () => {
  it('Detail-Panel zeigt Anzahl bei Mehrfach-Auswahl', async () => {
    const { findByLabelText, getByTestId, queryByTestId } = render()
    const a = await findByLabelText(/Auswählen.*a\.txt/i)
    const b = await findByLabelText(/Auswählen.*b\.txt/i)
    act(() => { fireEvent.click(a) })
    expect(queryByTestId('detail-multi')).toBeNull()
    act(() => { fireEvent.click(b) })
    expect(getByTestId('detail-multi')).toHaveTextContent('2 Einträge ausgewählt')
  })
})

describe('FilesPage — Multiselect (Slice 9: Bulk-Delete)', () => {
  it('Bulk-Delete ruft apiClient.files.delete pro ausgewählter Datei', async () => {
    const delSpy = vi.spyOn(apiClient.files, 'delete').mockResolvedValue(undefined as any)
    const { findByLabelText, findByRole, getByRole } = render()
    const a = await findByLabelText(/Auswählen.*a\.txt/i)
    const b = await findByLabelText(/Auswählen.*b\.txt/i)
    act(() => { fireEvent.click(a) })
    act(() => { fireEvent.click(b) })
    const delBtn = await findByRole('button', { name: 'Löschen' })
    act(() => { fireEvent.click(delBtn) })
    const confirm = getByRole('button', { name: 'In Papierkorb' })
    await act(async () => { fireEvent.click(confirm) })
    expect(delSpy).toHaveBeenCalledTimes(2)
    expect(delSpy.mock.calls.map((c) => c[1]).sort()).toEqual(['/a.txt', '/b.txt'])
  })
})

describe('FilesPage — Multiselect (Freiflächen-Klick)', () => {
  it('Mousedown auf leere Tabellen-Fläche hebt die Auswahl auf', async () => {
    const { findByLabelText, queryByTestId, container } = render()
    const a = await findByLabelText(/Auswählen.*a\.txt/i)
    const b = await findByLabelText(/Auswählen.*b\.txt/i)
    act(() => { fireEvent.click(a) })
    act(() => { fireEvent.click(b) })
    expect(queryByTestId('detail-multi')).not.toBeNull()
    const surface = container.querySelector('[data-files-content]') as HTMLElement
    expect(surface).toBeTruthy()
    act(() => { fireEvent.mouseDown(surface, { button: 0 }) })
    expect(queryByTestId('detail-multi')).toBeNull()
  })
})

describe('FilesPage — Multiselect (Slice 11: Path-Reset + Keyboard)', () => {
  it('Esc leert die Auswahl', async () => {
    const { findByLabelText, queryByTestId } = render()
    const a = await findByLabelText(/Auswählen.*a\.txt/i)
    const b = await findByLabelText(/Auswählen.*b\.txt/i)
    act(() => { fireEvent.click(a) })
    act(() => { fireEvent.click(b) })
    expect(queryByTestId('detail-multi')).not.toBeNull()
    act(() => { fireEvent.keyDown(window, { key: 'Escape' }) })
    expect(queryByTestId('detail-multi')).toBeNull()
  })

  it('Ctrl+A wählt alle sichtbaren Einträge', async () => {
    const { findByLabelText, getByTestId } = render()
    await findByLabelText(/Auswählen.*a\.txt/i)
    // The keydown listener re-registers as orderedKeys populates; on slower CI
    // a single fire can hit a closure that still holds an empty orderedKeys and
    // select nothing. Ctrl+A is idempotent (replaceSelection), so retry it
    // until the effect has the full key set and the panel shows all three.
    await waitFor(() => {
      fireEvent.keyDown(window, { key: 'a', ctrlKey: true })
      expect(getByTestId('detail-multi')).toHaveTextContent('3 Einträge ausgewählt')
    })
  })

  it('Header-Checkbox wählt eine volle Auswahl wieder ab (kein Lasso-Reselect)', async () => {
    const { findByLabelText, getByTestId, queryByTestId } = render()
    await findByLabelText(/Auswählen.*a\.txt/i)
    await waitFor(() => {
      fireEvent.keyDown(window, { key: 'a', ctrlKey: true })
      expect(getByTestId('detail-multi')).toHaveTextContent('3 Einträge ausgewählt')
    })
    // A real click is mousedown + click. The header checkbox sits outside the
    // lasso's [data-entry-key] rows, so its mousedown must not clear the
    // selection and restart the lasso — otherwise toggleAll re-selects all.
    const header = await findByLabelText('Alle auswählen')
    act(() => { fireEvent.mouseDown(header, { button: 0 }) })
    act(() => { fireEvent.click(header) })
    expect(queryByTestId('detail-multi')).toBeNull()
  })
})
