import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import type { SyncRoot, SharedMount } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { useFilesData } from './useFilesData'

function mount(id: string, name: string, path: string): SharedMount {
  return {
    id, name, path, is_file: false, can_write: false, expires_at: null,
    owner: { id: 'o', username: 'alice', avatar_url: null },
  }
}

beforeEach(() => { vi.restoreAllMocks() })

describe('useFilesData — loadFiles (mount mode)', () => {
  // A sync-root share has path "/". The mount endpoint returns giver-absolute
  // paths ("/foo"); stripping the "/" prefix must NOT eat the leading slash,
  // or buildEntries drops every row and the mount renders empty.
  it('keeps the leading slash when the mount IS a sync-root (path "/")', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([] as never)
    vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([mount('m1', 'Alices Root', '/')])
    vi.spyOn(apiClient.sharedMounts, 'files').mockResolvedValue({
      entries: [
        { name: 'Reports', path: '/Reports', is_dir: true,  size: 0 },
        { name: 'memo.txt', path: '/memo.txt', is_dir: false, size: 1 },
      ],
    })

    const { result } = renderHook(() =>
      useFilesData({ selectedRoot: null, currentPath: '/', mountMode: true, t: (k) => k }))

    await waitFor(() => expect(result.current.mounts).toHaveLength(1))
    await result.current.loadFiles('m1', '/')

    await waitFor(() =>
      expect(result.current.allFiles.map((f) => f.path)).toEqual(['/Reports', '/memo.txt']))
  })

  // A sub-folder share (path "/Team") still strips correctly: "/Team/Reports"
  // becomes the mount-relative "/Reports".
  it('strips the share-path prefix for a sub-folder mount', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([] as never)
    vi.spyOn(apiClient.sharedMounts, 'list').mockResolvedValue([mount('m1', 'Team', '/Team')])
    vi.spyOn(apiClient.sharedMounts, 'files').mockResolvedValue({
      entries: [{ name: 'Reports', path: '/Team/Reports', is_dir: true, size: 0 }],
    })

    const { result } = renderHook(() =>
      useFilesData({ selectedRoot: null, currentPath: '/', mountMode: true, t: (k) => k }))

    await waitFor(() => expect(result.current.mounts).toHaveLength(1))
    await result.current.loadFiles('m1', '/')

    await waitFor(() =>
      expect(result.current.allFiles.map((f) => f.path)).toEqual(['/Reports']))
  })
})
