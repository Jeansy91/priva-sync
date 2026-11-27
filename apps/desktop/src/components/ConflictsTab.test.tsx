import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../test/renderWithI18n'

const getConflicts = vi.fn()
const conflictVersions = vi.fn()
const conflictPreview = vi.fn()
const listSyncFailures = vi.fn()
const retrySyncFailure = vi.fn()
const resolveConflict = vi.fn()
const conflictDiff = vi.fn()

vi.mock('../tauri', () => ({
  cmd: {
    getConflicts: (...a: unknown[]) => getConflicts(...a),
    listSyncFailures: (...a: unknown[]) => listSyncFailures(...a),
    retrySyncFailure: (...a: unknown[]) => retrySyncFailure(...a),
    resolveConflict: (...a: unknown[]) => resolveConflict(...a),
    conflictDiff: (...a: unknown[]) => conflictDiff(...a),
    conflictVersions: (...a: unknown[]) => conflictVersions(...a),
    conflictPreview: (...a: unknown[]) => conflictPreview(...a),
  },
  onSyncConflict: () => Promise.resolve(() => {}),
  onSyncFailure: () => Promise.resolve(() => {}),
}))

import ConflictsTab from './ConflictsTab'
import { useAppStore } from '../store'

beforeEach(() => {
  getConflicts.mockReset().mockResolvedValue([])
  conflictVersions.mockReset().mockRejectedValue(new Error('no facts'))
  conflictPreview.mockReset().mockResolvedValue(null)
  listSyncFailures.mockReset().mockResolvedValue([])
  retrySyncFailure.mockReset()
  resolveConflict.mockReset()
  conflictDiff.mockReset()
  useAppStore.setState({ syncRoots: [], pathWarnings: [] })
})

describe('ConflictsTab', () => {
  it('shows the all-clear state when there is nothing to resolve', async () => {
    renderWithI18n(<ConflictsTab />)
    await waitFor(() => expect(screen.getByText('Keine Konflikte')).toBeInTheDocument())
  })

  it('lists a conflict and lets the user open the resolve dialog', async () => {
    getConflicts.mockResolvedValue([
      { sync_root_id: 'root-1', path: '/docs/report.txt' },
    ])
    useAppStore.setState({ syncRoots: [{ id: 'root-1', name: 'Dokumente' } as any], pathWarnings: [] })

    renderWithI18n(<ConflictsTab />)

    await waitFor(() => expect(screen.getByText('/docs/report.txt')).toBeInTheDocument())
    expect(screen.getByText('Dokumente')).toBeInTheDocument()

    fireEvent.click(screen.getByTitle('Lösen…'))
    expect(await screen.findByText('Konflikt lösen')).toBeInTheDocument()
  })

  it('lists a sync failure and retries it', async () => {
    listSyncFailures.mockResolvedValue([
      {
        kind: 'upload', root_id: 'root-1', path: '/a.bin',
        attempt_count: 2, next_retry_at: null, last_error: 'boom',
      },
    ])
    retrySyncFailure.mockResolvedValue(undefined)

    renderWithI18n(<ConflictsTab />)

    await waitFor(() => expect(screen.getByText('/a.bin')).toBeInTheDocument())
    expect(screen.getByText('boom')).toBeInTheDocument()

    fireEvent.click(screen.getByTitle('Sofort erneut versuchen'))
    await waitFor(() =>
      expect(retrySyncFailure).toHaveBeenCalledWith('upload', 'root-1', '/a.bin'),
    )
  })

  it('lists a path warning and dismisses it', async () => {
    useAppStore.setState({
      syncRoots: [],
      pathWarnings: [{ path: '/weird name', code: 'invalid_chars', blocking: true } as any],
    })

    renderWithI18n(<ConflictsTab />)

    await waitFor(() => expect(screen.getByText('/weird name')).toBeInTheDocument())

    fireEvent.click(screen.getByTitle('Schließen'))
    await waitFor(() => expect(useAppStore.getState().pathWarnings).toHaveLength(0))
  })

  it('shows an error banner when loading fails', async () => {
    getConflicts.mockRejectedValue(new Error('offline'))

    renderWithI18n(<ConflictsTab />)

    await waitFor(() => expect(screen.getByText(/offline/)).toBeInTheDocument())
  })
})
