import { describe, it, expect, vi } from 'vitest'
import { screen, fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { SyncRootListItem } from './SyncRootListItem'
import type { SyncRootEntry, SyncProgressEvent } from '../../tauri'

const root = {
  id: 'root-1', name: 'Dokumente', local_path: 'C:\\Users\\me\\Dokumente',
  download_mode: 'eager', paused: false, read_only: false,
} as SyncRootEntry

describe('SyncRootListItem', () => {
  it('shows the local path when nothing is transferring', () => {
    renderWithI18n(
      <SyncRootListItem
        root={root} progress={{}} supportsOnDemand={true}
        onToggleDownloadMode={() => {}} onTogglePaused={() => {}} onRemove={() => {}}
      />,
    )
    expect(screen.getByText('Dokumente')).toBeInTheDocument()
    expect(screen.getByText('C:\\Users\\me\\Dokumente')).toBeInTheDocument()
  })

  it('shows the active transfer file and progress fraction', () => {
    const progress: Record<string, SyncProgressEvent> = {
      'a.bin': { action: 'upload', file: '/docs/a.bin', current: 3, total: 10 } as SyncProgressEvent,
    }
    renderWithI18n(
      <SyncRootListItem
        root={root} progress={progress} supportsOnDemand={true}
        onToggleDownloadMode={() => {}} onTogglePaused={() => {}} onRemove={() => {}}
      />,
    )
    expect(screen.getByText('a.bin')).toBeInTheDocument()
    expect(screen.getByText('3/10')).toBeInTheDocument()
  })

  it('names the newest in-flight file, not the one that started first', () => {
    const progress: Record<string, SyncProgressEvent> = {
      '/docs/first.bin':  { action: 'download', file: '/docs/first.bin',  current: 1, total: 40 } as SyncProgressEvent,
      '/docs/newest.bin': { action: 'download', file: '/docs/newest.bin', current: 7, total: 40 } as SyncProgressEvent,
    }
    renderWithI18n(
      <SyncRootListItem
        root={root} progress={progress} supportsOnDemand={true}
        onToggleDownloadMode={() => {}} onTogglePaused={() => {}} onRemove={() => {}}
      />,
    )
    expect(screen.getByText('newest.bin')).toBeInTheDocument()
    expect(screen.queryByText('first.bin')).not.toBeInTheDocument()
    expect(screen.getByText('7/40')).toBeInTheDocument()
  })

  it('still names the newest file when every in-flight slot shares one counter', () => {
    // The counter advances per *completed* file now, so all eight parallel
    // uploads report the same `current`. Ordering by that value would leave
    // the label stuck on whichever slot happened to come first.
    const progress: Record<string, SyncProgressEvent> = {
      '/docs/older.bin': { action: 'upload', file: '/docs/older.bin', current: 12, total: 900 } as SyncProgressEvent,
      '/docs/later.bin': { action: 'upload', file: '/docs/later.bin', current: 12, total: 900 } as SyncProgressEvent,
    }
    renderWithI18n(
      <SyncRootListItem
        root={root} progress={progress} supportsOnDemand={true}
        onToggleDownloadMode={() => {}} onTogglePaused={() => {}} onRemove={() => {}}
      />,
    )
    expect(screen.getByText('later.bin')).toBeInTheDocument()
    expect(screen.queryByText('older.bin')).not.toBeInTheDocument()
    expect(screen.getByText('12/900')).toBeInTheDocument()
  })

  it('shows the paused badge and flips the pause/resume button', () => {
    const onTogglePaused = vi.fn()
    renderWithI18n(
      <SyncRootListItem
        root={{ ...root, paused: true }} progress={{}} supportsOnDemand={true}
        onToggleDownloadMode={() => {}} onTogglePaused={onTogglePaused} onRemove={() => {}}
      />,
    )
    expect(screen.getByText('Pausiert')).toBeInTheDocument()
    fireEvent.click(screen.getByTitle('Fortsetzen'))
    expect(onTogglePaused).toHaveBeenCalledWith({ ...root, paused: true })
  })

  it('hides the on-demand toggle for read-only roots', () => {
    renderWithI18n(
      <SyncRootListItem
        root={{ ...root, read_only: true }} progress={{}} supportsOnDemand={true}
        onToggleDownloadMode={() => {}} onTogglePaused={() => {}} onRemove={() => {}}
      />,
    )
    expect(screen.getByText('Nur-Lesen')).toBeInTheDocument()
    expect(screen.queryByTitle('Sofort (Klicken für On-Demand)')).not.toBeInTheDocument()
  })

  it('calls onRemove when the trash button is clicked', () => {
    const onRemove = vi.fn()
    renderWithI18n(
      <SyncRootListItem
        root={root} progress={{}} supportsOnDemand={true}
        onToggleDownloadMode={() => {}} onTogglePaused={() => {}} onRemove={onRemove}
      />,
    )
    fireEvent.click(screen.getByTitle('Entfernen'))
    expect(onRemove).toHaveBeenCalledWith(root)
  })

  it('toggles download mode', () => {
    const onToggleDownloadMode = vi.fn()
    renderWithI18n(
      <SyncRootListItem
        root={root} progress={{}} supportsOnDemand={true}
        onToggleDownloadMode={onToggleDownloadMode} onTogglePaused={() => {}} onRemove={() => {}}
      />,
    )
    fireEvent.click(screen.getByTitle('Sofort (Klicken für On-Demand)'))
    expect(onToggleDownloadMode).toHaveBeenCalledWith(root)
  })
})
