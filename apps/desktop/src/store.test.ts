import { describe, it, expect, beforeEach } from 'vitest'
import { useAppStore } from './store'
import type { SyncProgressEvent, PathWarning } from './tauri'

const initialState = useAppStore.getState()

beforeEach(() => {
  useAppStore.setState(initialState, true)
})

describe('useAppStore', () => {
  it('setConfig/setStatus/setSyncRoots update their slice', () => {
    useAppStore.getState().setConfig({ server_url: 'http://x', username: 'u' } as any)
    useAppStore.getState().setStatus({ status: 'syncing' } as any)
    useAppStore.getState().setSyncRoots([{ id: '1' } as any])

    const s = useAppStore.getState()
    expect(s.config).toEqual({ server_url: 'http://x', username: 'u' })
    expect(s.status).toEqual({ status: 'syncing' })
    expect(s.syncRoots).toEqual([{ id: '1' }])
  })

  it('setRootProgress adds a file entry under its root', () => {
    const event: SyncProgressEvent = { root_id: 'r1', file: '/a.txt', action: 'upload' } as any
    useAppStore.getState().setRootProgress(event)

    expect(useAppStore.getState().rootProgress).toEqual({ r1: { '/a.txt': event } })
  })

  it('setRootProgress "file_done" removes just that file, keeps siblings', () => {
    const a: SyncProgressEvent = { root_id: 'r1', file: '/a.txt', action: 'upload' } as any
    const b: SyncProgressEvent = { root_id: 'r1', file: '/b.txt', action: 'upload' } as any
    useAppStore.getState().setRootProgress(a)
    useAppStore.getState().setRootProgress(b)

    useAppStore.getState().setRootProgress({ root_id: 'r1', file: '/a.txt', action: 'file_done' } as any)

    expect(useAppStore.getState().rootProgress).toEqual({ r1: { '/b.txt': b } })
  })

  it('setRootProgress "file_done" on the last file clears the whole root entry', () => {
    const a: SyncProgressEvent = { root_id: 'r1', file: '/a.txt', action: 'upload' } as any
    useAppStore.getState().setRootProgress(a)

    useAppStore.getState().setRootProgress({ root_id: 'r1', file: '/a.txt', action: 'file_done' } as any)

    expect(useAppStore.getState().rootProgress).toEqual({})
  })

  it('setRootProgress "done" clears all files for that root regardless of count', () => {
    useAppStore.getState().setRootProgress({ root_id: 'r1', file: '/a.txt', action: 'upload' } as any)
    useAppStore.getState().setRootProgress({ root_id: 'r1', file: '/b.txt', action: 'upload' } as any)
    useAppStore.getState().setRootProgress({ root_id: 'r2', file: '/c.txt', action: 'upload' } as any)

    useAppStore.getState().setRootProgress({ root_id: 'r1', file: '', action: 'done' } as any)

    expect(useAppStore.getState().rootProgress).toEqual({ r2: { '/c.txt': { root_id: 'r2', file: '/c.txt', action: 'upload' } } })
  })

  it('addPathWarning is deduplicated by path', () => {
    const w: PathWarning = { path: '/too/long', reason: 'too_long' } as any
    useAppStore.getState().addPathWarning(w)
    useAppStore.getState().addPathWarning({ ...w })

    expect(useAppStore.getState().pathWarnings).toHaveLength(1)
  })

  it('addPathWarning keeps distinct paths', () => {
    useAppStore.getState().addPathWarning({ path: '/a', reason: 'too_long' } as any)
    useAppStore.getState().addPathWarning({ path: '/b', reason: 'too_long' } as any)

    expect(useAppStore.getState().pathWarnings).toHaveLength(2)
  })

  it('dismissPathWarning removes only the matching path', () => {
    useAppStore.getState().addPathWarning({ path: '/a', reason: 'too_long' } as any)
    useAppStore.getState().addPathWarning({ path: '/b', reason: 'too_long' } as any)

    useAppStore.getState().dismissPathWarning('/a')

    expect(useAppStore.getState().pathWarnings.map((w) => w.path)).toEqual(['/b'])
  })
})
