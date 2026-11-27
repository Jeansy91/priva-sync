import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { FolderTreePicker } from './FolderTreePicker'
import { apiClient } from '../../store/auth'
import type { FileEntry } from '@privasync/api-client'

function dirEntry(path: string): FileEntry {
  return {
    id: path, path, size: 0, mtime: new Date().toISOString(),
    content_hash: '', is_deleted: false, updated_at: new Date().toISOString(),
    is_dir: true,
  } as unknown as FileEntry
}

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('FolderTreePicker', () => {
  it('laedt den Wurzel-Ordner und zeigt seine Unterordner', async () => {
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({
      files: [dirEntry('/Bilder'), dirEntry('/Dokumente')],
    } as any)
    const { findByText, getByText } = renderWithI18n(
      <FolderTreePicker rootID="r-1" rootName="Meine Dateien" value="/" onChange={() => {}} />,
      { lang: 'de' },
    )
    await findByText('Bilder')
    expect(getByText('Dokumente')).toBeInTheDocument()
    expect(getByText('Meine Dateien')).toBeInTheDocument()
  })

  it('klappt Unterordner beim Klick auf den Expand-Button aus und laedt sie nach', async () => {
    vi.spyOn(apiClient.files, 'list').mockImplementation(async (_root, path) => {
      if (path === '/') return { files: [dirEntry('/Bilder')] } as any
      if (path === '/Bilder') return { files: [dirEntry('/Bilder/Urlaub')] } as any
      return { files: [] } as any
    })
    const { findByText, findByLabelText } = renderWithI18n(
      <FolderTreePicker rootID="r-1" rootName="root" value="/" onChange={() => {}} />,
      { lang: 'de' },
    )
    await findByText('Bilder')
    fireEvent.click(await findByLabelText('Bilder erweitern'))
    await findByText('Urlaub')
  })

  it('ruft onChange mit dem Pfad auf, wenn ein Ordner ausgewaehlt wird', async () => {
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({
      files: [dirEntry('/Bilder')],
    } as any)
    const onChange = vi.fn()
    const { findByText } = renderWithI18n(
      <FolderTreePicker rootID="r-1" rootName="root" value="/" onChange={onChange} />,
      { lang: 'de' },
    )
    fireEvent.click(await findByText('Bilder'))
    expect(onChange).toHaveBeenCalledWith('/Bilder')
  })

  it('deaktiviert Ordner aus disabledPaths', async () => {
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({
      files: [dirEntry('/Bilder')],
    } as any)
    const onChange = vi.fn()
    const { findByText } = renderWithI18n(
      <FolderTreePicker
        rootID="r-1" rootName="root" value="/" onChange={onChange}
        disabledPaths={new Set(['/Bilder'])}
      />,
      { lang: 'de' },
    )
    const label = await findByText('Bilder')
    fireEvent.click(label)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('erstellt ueber das Inline-Formular einen neuen Ordner und waehlt ihn aus', async () => {
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [] } as any)
    const createSpy = vi.spyOn(apiClient.files, 'createDirectory').mockResolvedValue(undefined as any)
    const onChange = vi.fn()
    const onFolderCreated = vi.fn()
    const { findByText, findByLabelText, findByPlaceholderText, findByRole } = renderWithI18n(
      <FolderTreePicker
        rootID="r-1" rootName="root" value="/" onChange={onChange}
        onFolderCreated={onFolderCreated}
      />,
      { lang: 'de' },
    )
    await findByText('root')
    fireEvent.click(await findByLabelText('Neuer Unterordner'))
    const input = await findByPlaceholderText('Ordnername')
    fireEvent.change(input, { target: { value: 'Neu' } })
    fireEvent.click(await findByRole('button', { name: 'Erstellen' }))

    await vi.waitFor(() => {
      expect(createSpy).toHaveBeenCalledWith('r-1', '/Neu')
      expect(onChange).toHaveBeenCalledWith('/Neu')
      expect(onFolderCreated).toHaveBeenCalledWith('/Neu')
    })
  })

  it('zeigt einen Validierungsfehler bei leerem Namen und ruft die API nicht auf', async () => {
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [] } as any)
    const createSpy = vi.spyOn(apiClient.files, 'createDirectory')
    const { findByText, findByLabelText, findByRole } = renderWithI18n(
      <FolderTreePicker rootID="r-1" rootName="root" value="/" onChange={() => {}} />,
      { lang: 'de' },
    )
    await findByText('root')
    fireEvent.click(await findByLabelText('Neuer Unterordner'))
    fireEvent.click(await findByRole('button', { name: 'Erstellen' }))
    await findByText('Bitte einen Namen eingeben.')
    expect(createSpy).not.toHaveBeenCalled()
  })

  it('zeigt einen Server-Fehler, wenn das Erstellen fehlschlaegt', async () => {
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [] } as any)
    vi.spyOn(apiClient.files, 'createDirectory').mockRejectedValue({ body: { error: 'quota exceeded' } })
    const { findByText, findByLabelText, findByPlaceholderText, findByRole } = renderWithI18n(
      <FolderTreePicker rootID="r-1" rootName="root" value="/" onChange={() => {}} />,
      { lang: 'de' },
    )
    await findByText('root')
    fireEvent.click(await findByLabelText('Neuer Unterordner'))
    fireEvent.change(await findByPlaceholderText('Ordnername'), { target: { value: 'X' } })
    fireEvent.click(await findByRole('button', { name: 'Erstellen' }))
    await findByText('quota exceeded')
  })

  it('bricht das Inline-Formular per Escape ab', async () => {
    vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [] } as any)
    const { findByText, findByLabelText, findByPlaceholderText, queryByPlaceholderText } = renderWithI18n(
      <FolderTreePicker rootID="r-1" rootName="root" value="/" onChange={() => {}} />,
      { lang: 'de' },
    )
    await findByText('root')
    fireEvent.click(await findByLabelText('Neuer Unterordner'))
    const input = await findByPlaceholderText('Ordnername')
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(queryByPlaceholderText('Ordnername')).not.toBeInTheDocument()
  })

  it('preload die Vorfahren des aktuellen Werts', async () => {
    const listSpy = vi.spyOn(apiClient.files, 'list').mockResolvedValue({ files: [] } as any)
    renderWithI18n(
      <FolderTreePicker rootID="r-1" rootName="root" value="/A/B" onChange={() => {}} />,
      { lang: 'de' },
    )
    await vi.waitFor(() => {
      expect(listSpy).toHaveBeenCalledWith('r-1', '/')
      expect(listSpy).toHaveBeenCalledWith('r-1', '/A')
    })
  })
})
