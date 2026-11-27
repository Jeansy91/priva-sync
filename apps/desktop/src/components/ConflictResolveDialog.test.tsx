import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../test/renderWithI18n'

const resolveConflict = vi.fn()
const conflictDiff = vi.fn()
const conflictVersions = vi.fn()
const conflictPreview = vi.fn()

vi.mock('../tauri', () => ({
  cmd: {
    resolveConflict: (...a: unknown[]) => resolveConflict(...a),
    conflictDiff: (...a: unknown[]) => conflictDiff(...a),
    conflictVersions: (...a: unknown[]) => conflictVersions(...a),
    conflictPreview: (...a: unknown[]) => conflictPreview(...a),
  },
}))

import ConflictResolveDialog from './ConflictResolveDialog'

const target = { rootId: 'root-1', path: '/docs/report.txt' }

beforeEach(() => {
  resolveConflict.mockReset()
  conflictDiff.mockReset()
  conflictVersions.mockReset().mockRejectedValue(new Error('no facts'))
  conflictPreview.mockReset().mockResolvedValue(null)
})

describe('ConflictResolveDialog', () => {
  it('renders nothing (dialog closed) when target is null', () => {
    renderWithI18n(<ConflictResolveDialog target={null} onClose={vi.fn()} onResolved={vi.fn()} />)
    expect(screen.queryByText('Konflikt lösen')).not.toBeInTheDocument()
  })

  it('resolves with keep_current and calls onResolved + onClose', async () => {
    resolveConflict.mockResolvedValue(undefined)
    const onClose = vi.fn()
    const onResolved = vi.fn()
    renderWithI18n(<ConflictResolveDialog target={target} onClose={onClose} onResolved={onResolved} />)

    fireEvent.click(screen.getByRole('button', { name: 'Server-Version behalten' }))

    await waitFor(() => expect(resolveConflict).toHaveBeenCalledWith('root-1', '/docs/report.txt', 'keep_current'))
    await waitFor(() => expect(onResolved).toHaveBeenCalled())
    expect(onClose).toHaveBeenCalled()
  })

  it('resolves with keep_both', async () => {
    resolveConflict.mockResolvedValue(undefined)
    renderWithI18n(<ConflictResolveDialog target={target} onClose={vi.fn()} onResolved={vi.fn()} />)

    fireEvent.click(screen.getByRole('button', { name: 'Beide behalten' }))

    await waitFor(() => expect(resolveConflict).toHaveBeenCalledWith('root-1', '/docs/report.txt', 'keep_both'))
  })

  it('shows an error message when resolve fails and keeps the dialog open', async () => {
    resolveConflict.mockRejectedValue(new Error('disk full'))
    const onClose = vi.fn()
    renderWithI18n(<ConflictResolveDialog target={target} onClose={onClose} onResolved={vi.fn()} />)

    fireEvent.click(screen.getByRole('button', { name: 'Lokale Version übernehmen' }))

    await waitFor(() => expect(screen.getByText('Konflikt konnte nicht gelöst werden.')).toBeInTheDocument())
    expect(onClose).not.toHaveBeenCalled()
  })

  it('loads and displays a text diff on demand', async () => {
    conflictDiff.mockResolvedValue({
      kind: 'lines',
      lines: [
        { kind: 'unchanged', text: 'same line' },
        { kind: 'added', text: 'new line' },
        { kind: 'removed', text: 'old line' },
      ],
    })
    renderWithI18n(<ConflictResolveDialog target={target} onClose={vi.fn()} onResolved={vi.fn()} />)

    fireEvent.click(screen.getByRole('button', { name: 'Vergleich anzeigen' }))

    await waitFor(() => expect(screen.getByText('new line')).toBeInTheDocument())
    expect(screen.getByText('old line')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Vergleich ausblenden' })).toBeInTheDocument()
  })

  it('shows a binary-diff notice when the diff is not text', async () => {
    conflictDiff.mockResolvedValue({ kind: 'binary' })
    renderWithI18n(<ConflictResolveDialog target={target} onClose={vi.fn()} onResolved={vi.fn()} />)

    fireEvent.click(screen.getByRole('button', { name: 'Vergleich anzeigen' }))

    await waitFor(() =>
      expect(screen.getByText('Kein Text-Vergleich möglich (Binärdatei).')).toBeInTheDocument(),
    )
  })

  it('shows a failure notice when the diff request rejects', async () => {
    conflictDiff.mockRejectedValue(new Error('network error'))
    renderWithI18n(<ConflictResolveDialog target={target} onClose={vi.fn()} onResolved={vi.fn()} />)

    fireEvent.click(screen.getByRole('button', { name: 'Vergleich anzeigen' }))

    await waitFor(() =>
      expect(screen.getByText('Vergleich konnte nicht geladen werden.')).toBeInTheDocument(),
    )
  })

  it('calls onClose when Cancel is clicked', () => {
    const onClose = vi.fn()
    renderWithI18n(<ConflictResolveDialog target={target} onClose={onClose} onResolved={vi.fn()} />)

    fireEvent.click(screen.getByRole('button', { name: 'Abbrechen' }))
    expect(onClose).toHaveBeenCalled()
  })

  describe('Versionsvergleich', () => {
    const facts = (size: number, modified_ms: number | null) => ({ size, modified_ms })

    it('zeigt exakte Größe und Datum mit Sekunden beider Versionen', async () => {
      conflictVersions.mockResolvedValue({
        current: facts(1234567, Date.UTC(2026, 10, 3, 14, 5, 9)),
        incoming: facts(1234568, Date.UTC(2026, 10, 3, 14, 5, 14)),
        incoming_name: 'report.conflict-20261103T140514.txt',
      })
      renderWithI18n(<ConflictResolveDialog target={target} onClose={vi.fn()} onResolved={vi.fn()} />)

      expect(await screen.findByText('1.234.567 Byte')).toBeInTheDocument()
      expect(screen.getByText('1.234.568 Byte')).toBeInTheDocument()
      // Zeitzone des Testlaufs ist offen, die Sekunden nicht.
      expect(screen.getByText(/\d{2}:\d{2}:09/)).toBeInTheDocument()
      expect(screen.getByText(/\d{2}:\d{2}:14/)).toBeInTheDocument()
      expect(screen.getByText('report.conflict-20261103T140514.txt')).toBeInTheDocument()
    })

    it('weist auf identischen Inhalt hin, wenn Größe und Datum übereinstimmen', async () => {
      const same = facts(42, Date.UTC(2026, 10, 3, 14, 5, 9))
      conflictVersions.mockResolvedValue({ current: same, incoming: same, incoming_name: 'x' })
      renderWithI18n(<ConflictResolveDialog target={target} onClose={vi.fn()} onResolved={vi.fn()} />)

      expect(await screen.findByText(/sehr wahrscheinlich derselbe Inhalt/)).toBeInTheDocument()
    })

    it('zeigt bei Bildern beide Versionen nebeneinander', async () => {
      conflictVersions.mockResolvedValue({ current: facts(1, 0), incoming: facts(2, 0), incoming_name: 'x' })
      conflictPreview.mockResolvedValue({ current: 'data:image/jpeg;base64,AAA', incoming: 'data:image/jpeg;base64,BBB' })
      renderWithI18n(<ConflictResolveDialog target={target} onClose={vi.fn()} onResolved={vi.fn()} />)

      const images = await screen.findAllByRole('img')
      expect(images.map((i) => i.getAttribute('src'))).toEqual([
        'data:image/jpeg;base64,AAA',
        'data:image/jpeg;base64,BBB',
      ])
    })

    it('bleibt bedienbar, wenn die Dateiangaben nicht gelesen werden können', async () => {
      renderWithI18n(<ConflictResolveDialog target={target} onClose={vi.fn()} onResolved={vi.fn()} />)

      await waitFor(() => expect(conflictVersions).toHaveBeenCalled())
      expect(screen.getByText('/docs/report.txt')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Server-Version behalten' })).toBeEnabled()
    })
  })
})
