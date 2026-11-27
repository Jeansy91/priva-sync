import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { renderWithI18n } from '../test/renderWithI18n'
import ConflictResolveDialog from './ConflictResolveDialog'
import { apiClient } from '../store/auth'

const versions = {
  id: 'c-1', file_id: 'f-1', file_path: '/foo.txt',
  conflict_path: '/foo.conflict-2026.txt',
  created_at: '2026-06-01T10:00:00Z',
  current:  { size: 6,  mtime: '2026-06-01T09:00:00Z' },
  conflict: { size: 6, mtime: null, created_at: '2026-06-01T10:00:00Z' },
}

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(apiClient.files, 'getConflict').mockResolvedValue(versions)
})

function renderDialog() {
  return renderWithI18n(
    <ConflictResolveDialog
      conflict={{ id: 'c-1', rootId: 'r-1' }}
      localeTag="de-DE"
      onClose={() => {}}
      onResolved={() => {}}
    />,
    { lang: 'de' },
  )
}

describe('ConflictResolveDialog — Text-Diff', () => {
  it('zeigt Zeilen-Diff beider Versionen nach Klick auf Vergleichen', async () => {
    vi.spyOn(apiClient.files, 'download').mockResolvedValue(
      new Response('a\nx\nc') as unknown as Response,
    )
    vi.spyOn(apiClient.files, 'conflictFile').mockResolvedValue(
      new Response('a\ny\nc') as unknown as Response,
    )

    const { findByRole, findByText } = renderDialog()
    const btn = await findByRole('button', { name: 'Vergleich anzeigen' })
    await act(async () => { btn.click() })

    // Removed line from the current version, added line from the conflict copy.
    expect(await findByText('x')).toBeInTheDocument()
    expect(await findByText('y')).toBeInTheDocument()
    expect(await findByText('a')).toBeInTheDocument()
  })

  it('meldet Binärdateien statt Diff', async () => {
    vi.spyOn(apiClient.files, 'download').mockResolvedValue(
      new Response('PK' + String.fromCharCode(0) + 'bin') as unknown as Response,
    )
    vi.spyOn(apiClient.files, 'conflictFile').mockResolvedValue(
      new Response('text') as unknown as Response,
    )

    const { findByRole, findByText } = renderDialog()
    const btn = await findByRole('button', { name: 'Vergleich anzeigen' })
    await act(async () => { btn.click() })

    expect(await findByText('Kein Text-Vergleich möglich (Binärdatei).')).toBeInTheDocument()
  })
})

describe('ConflictResolveDialog — Versionsvergleich', () => {
  it('zeigt exakte Größe und Änderungsdatum mit Sekunden für beide Versionen', async () => {
    vi.spyOn(apiClient.files, 'getConflict').mockResolvedValue({
      ...versions,
      current:  { size: 1234567, mtime: '2026-06-01T09:00:00Z' },
      conflict: { size: 1234568, mtime: '2026-06-01T09:00:05Z', created_at: '2026-06-01T10:00:00Z' },
    })

    const { findByText } = renderDialog()

    expect(await findByText('1.234.567 Byte')).toBeInTheDocument()
    expect(await findByText('1.234.568 Byte')).toBeInTheDocument()
    // Zeitzone des Testlaufs ist offen, die Sekunden nicht.
    expect(await findByText(/\d{2}:\d{2}:05/)).toBeInTheDocument()
  })

  it('weist auf identischen Inhalt hin, wenn Größe und Datum übereinstimmen', async () => {
    vi.spyOn(apiClient.files, 'getConflict').mockResolvedValue({
      ...versions,
      conflict: { size: 6, mtime: '2026-06-01T09:00:00Z', created_at: '2026-06-01T10:00:00Z' },
    })

    const { findByText } = renderDialog()

    expect(await findByText(/sehr wahrscheinlich derselbe Inhalt/)).toBeInTheDocument()
  })

  it('behauptet bei fehlendem Datum der Konfliktkopie keine Übereinstimmung', async () => {
    const { findByText, queryByText } = renderDialog()

    expect(await findByText('Erkannt')).toBeInTheDocument()
    expect(queryByText(/sehr wahrscheinlich derselbe Inhalt/)).not.toBeInTheDocument()
  })

  it('zeigt bei Bildern beide Versionen nebeneinander', async () => {
    vi.spyOn(apiClient.files, 'getConflict').mockResolvedValue({
      ...versions, file_path: '/foto.JPG', conflict_path: '/foto.conflict-1.JPG',
    })
    vi.spyOn(apiClient.files, 'download').mockResolvedValue(
      new Response('a', { headers: { 'Content-Type': 'image/jpeg' } }) as unknown as Response,
    )
    vi.spyOn(apiClient.files, 'conflictFile').mockResolvedValue(
      new Response('b', { headers: { 'Content-Type': 'image/jpeg' } }) as unknown as Response,
    )
    let n = 0
    URL.createObjectURL = vi.fn(() => `blob:preview-${++n}`)
    URL.revokeObjectURL = vi.fn()

    const { findAllByRole } = renderDialog()

    const images = await findAllByRole('img')
    expect(images.map((i) => i.getAttribute('src'))).toEqual(['blob:preview-1', 'blob:preview-2'])
  })

  it('lädt für Nicht-Bilder keine Vorschau', async () => {
    const download = vi.spyOn(apiClient.files, 'download')

    const { findByText } = renderDialog()
    await findByText('Aktuelle Version')

    expect(download).not.toHaveBeenCalled()
  })
})
