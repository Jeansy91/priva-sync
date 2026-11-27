import { describe, it, expect, vi } from 'vitest'
import { screen, fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { RemoveSyncRootDialog } from './RemoveSyncRootDialog'
import type { SyncRootEntry } from '../../tauri'

const root = { id: 'root-1', name: 'Dokumente', download_mode: 'on_demand' } as SyncRootEntry

describe('RemoveSyncRootDialog', () => {
  it('renders nothing when no root is set', () => {
    const { container } = renderWithI18n(
      <RemoveSyncRootDialog
        root={null} options={null} busy={false} info=""
        onClose={() => {}} onConfirm={() => {}} onDownloadFirst={() => {}}
      />,
    )
    expect(container.textContent).toBe('')
  })

  it('shows the checking state while options are loading', () => {
    renderWithI18n(
      <RemoveSyncRootDialog
        root={root} options={null} busy={false} info=""
        onClose={() => {}} onConfirm={() => {}} onDownloadFirst={() => {}}
      />,
    )
    expect(screen.getByText('Prüfe lokale Dateien…')).toBeInTheDocument()
  })

  it('warns about dehydrated placeholders and offers download-first', () => {
    const onDownloadFirst = vi.fn()
    renderWithI18n(
      <RemoveSyncRootDialog
        root={root}
        options={{ dehydrated_count: 3, dehydrated_bytes: 1024 * 1024 } as any}
        busy={false} info=""
        onClose={() => {}} onConfirm={() => {}} onDownloadFirst={onDownloadFirst}
      />,
    )
    expect(screen.getByText(/3 Dateien/)).toBeInTheDocument()

    fireEvent.click(screen.getByText('Erst alles herunterladen'))
    expect(onDownloadFirst).toHaveBeenCalled()
  })

  it('does not offer download-first once the root is already eager', () => {
    renderWithI18n(
      <RemoveSyncRootDialog
        root={{ ...root, download_mode: 'eager' } as SyncRootEntry}
        options={{ dehydrated_count: 3, dehydrated_bytes: 1024 } as any}
        busy={false} info=""
        onClose={() => {}} onConfirm={() => {}} onDownloadFirst={() => {}}
      />,
    )
    expect(screen.queryByText('Erst alles herunterladen')).not.toBeInTheDocument()
  })

  it('confirms removal', () => {
    const onConfirm = vi.fn()
    renderWithI18n(
      <RemoveSyncRootDialog
        root={root} options={{ dehydrated_count: 0, dehydrated_bytes: 0 } as any}
        busy={false} info=""
        onClose={() => {}} onConfirm={onConfirm} onDownloadFirst={() => {}}
      />,
    )
    fireEvent.click(screen.getByText('Entfernen'))
    expect(onConfirm).toHaveBeenCalled()
  })

  it('shows info text and swaps the close button label once removal is done', () => {
    renderWithI18n(
      <RemoveSyncRootDialog
        root={root} options={{ dehydrated_count: 0, dehydrated_bytes: 0 } as any}
        busy={false} info="Sync-Ordner entfernt."
        onClose={() => {}} onConfirm={() => {}} onDownloadFirst={() => {}}
      />,
    )
    expect(screen.getByText('Sync-Ordner entfernt.')).toBeInTheDocument()
    expect(screen.getByText('Schließen')).toBeInTheDocument()
    expect(screen.queryByText('Entfernen')).not.toBeInTheDocument()
  })
})
