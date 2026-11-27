import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import type { SharedMount, FileEntry } from '@privasync/api-client'
import { apiClient } from '../../../store/auth'
import { useMountStats } from './useMountStats'
import type { DetailSelection } from '../FileDetailPanel'

function mount(over: Partial<SharedMount> = {}): SharedMount {
  return {
    id: 'm1', name: 'Team Root', path: '/', is_file: false, can_write: false,
    expires_at: null, owner: { id: 'o', username: 'alice', avatar_url: null },
    ...over,
  }
}

function fileEntry(over: Partial<FileEntry> = {}): FileEntry {
  return {
    id: 'f1', path: '/a.txt', size: 0, mtime: '2026-01-01T00:00:00Z',
    content_hash: '', is_deleted: false, updated_at: '2026-01-01T00:00:00Z',
    ...over,
  }
}

beforeEach(() => { vi.restoreAllMocks() })

describe('useMountStats', () => {
  it('does nothing without a mount on the selection', () => {
    const statsSpy = vi.spyOn(apiClient.sharedMounts, 'stats')
    renderHook(() => useMountStats(null, vi.fn()))
    expect(statsSpy).not.toHaveBeenCalled()
  })

  it('folds file stats into a matching file selection', async () => {
    vi.spyOn(apiClient.sharedMounts, 'stats').mockResolvedValue({
      kind: 'file', id: 'f1', size: 42, mtime: '2026-02-01T00:00:00Z',
      content_hash: 'abc', updated_at: '2026-02-01T00:00:00Z',
    } as never)

    const selection: DetailSelection = { kind: 'file', file: fileEntry(), mount: mount() }
    let current: DetailSelection | null = selection
    const setDetailSelection = vi.fn((updater: (prev: DetailSelection | null) => DetailSelection | null) => {
      current = updater(current)
    })

    renderHook(() => useMountStats(current, setDetailSelection))

    await waitFor(() => expect(setDetailSelection).toHaveBeenCalled())
    expect(current).toMatchObject({ kind: 'file', file: { size: 42, content_hash: 'abc' } })
  })

  it('folds dir stats into a matching dir selection', async () => {
    vi.spyOn(apiClient.sharedMounts, 'stats').mockResolvedValue({
      kind: 'dir', item_count: 3, total_size: 900, latest_mtime: '2026-03-01T00:00:00Z',
    } as never)

    const selection: DetailSelection = {
      kind: 'dir', name: 'Reports', path: '/Reports',
      itemCount: 0, totalSize: 0, latestMtime: null, mount: mount(),
    }
    let current: DetailSelection | null = selection
    const setDetailSelection = vi.fn((updater: (prev: DetailSelection | null) => DetailSelection | null) => {
      current = updater(current)
    })

    renderHook(() => useMountStats(current, setDetailSelection))

    await waitFor(() => expect(setDetailSelection).toHaveBeenCalled())
    expect(current).toMatchObject({ kind: 'dir', itemCount: 3, totalSize: 900 })
  })

  it('defaults a missing latest_mtime to null', async () => {
    vi.spyOn(apiClient.sharedMounts, 'stats').mockResolvedValue({
      kind: 'dir', item_count: 0, total_size: 0, latest_mtime: undefined,
    } as never)

    const selection: DetailSelection = {
      kind: 'dir', name: 'Empty', path: '/Empty',
      itemCount: 0, totalSize: 0, latestMtime: null, mount: mount(),
    }
    let current: DetailSelection | null = selection
    const setDetailSelection = vi.fn((updater: (prev: DetailSelection | null) => DetailSelection | null) => {
      current = updater(current)
    })

    renderHook(() => useMountStats(current, setDetailSelection))

    await waitFor(() => expect(setDetailSelection).toHaveBeenCalled())
    expect((current as Extract<DetailSelection, { kind: 'dir' }>).latestMtime).toBeNull()
  })

  it('leaves the selection untouched when the kind does not match the response', async () => {
    vi.spyOn(apiClient.sharedMounts, 'stats').mockResolvedValue({
      kind: 'dir', item_count: 1, total_size: 1, latest_mtime: null,
    } as never)

    const selection: DetailSelection = { kind: 'file', file: fileEntry(), mount: mount() }
    let current: DetailSelection | null = selection
    const setDetailSelection = vi.fn((updater: (prev: DetailSelection | null) => DetailSelection | null) => {
      current = updater(current)
    })

    renderHook(() => useMountStats(current, setDetailSelection))

    await waitFor(() => expect(setDetailSelection).toHaveBeenCalled())
    expect(current).toEqual(selection)
  })

  it('keeps stub state when the request rejects', async () => {
    vi.spyOn(apiClient.sharedMounts, 'stats').mockRejectedValue(new Error('boom'))

    const selection: DetailSelection = { kind: 'file', file: fileEntry(), mount: mount() }
    const setDetailSelection = vi.fn()

    renderHook(() => useMountStats(selection, setDetailSelection))

    await waitFor(() => expect(apiClient.sharedMounts.stats).toHaveBeenCalled())
    expect(setDetailSelection).not.toHaveBeenCalled()
  })

  it('discards a stale response when the mount changes before it resolves', async () => {
    let resolveStats: (v: Awaited<ReturnType<typeof apiClient.sharedMounts.stats>>) => void = () => {}
    vi.spyOn(apiClient.sharedMounts, 'stats').mockImplementation(
      () => new Promise((resolve) => { resolveStats = resolve }),
    )
    const setDetailSelection = vi.fn()
    const selection: DetailSelection = { kind: 'file', file: fileEntry(), mount: mount({ id: 'm1' }) }

    const { unmount } = renderHook(() => useMountStats(selection, setDetailSelection))
    unmount()
    resolveStats({ kind: 'file', id: 'f1', size: 1, mtime: '', content_hash: '', updated_at: '' })
    await Promise.resolve()

    expect(setDetailSelection).not.toHaveBeenCalled()
  })
})
