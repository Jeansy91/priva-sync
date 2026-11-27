import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import type { SharedMount } from '@privasync/api-client'
import { apiClient } from '../../../store/auth'
import { useMountActions } from './useMountActions'

function mount(over: Partial<SharedMount> = {}): SharedMount {
  return {
    id: 'm1', name: 'Team Root', path: '/', is_file: false, can_write: false,
    expires_at: null, owner: { id: 'o', username: 'alice', avatar_url: null },
    ...over,
  }
}

function spyOnAppendChild() {
  let captured: HTMLAnchorElement | undefined
  const clickSpy = vi.fn()
  vi.spyOn(document.body, 'appendChild').mockImplementation((node) => {
    captured = node as HTMLAnchorElement
    Object.defineProperty(captured, 'click', { value: clickSpy, configurable: true })
    return node
  })
  vi.spyOn(document.body, 'removeChild').mockImplementation((node) => node)
  return { getAnchor: () => captured, clickSpy }
}

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('useMountActions — downloadMountFile', () => {
  // The browser's download manager fetches the token URL itself — the page
  // never buffers the file (ADR-0073).
  it('hands the token URL to a native download anchor', async () => {
    const urlSpy = vi.spyOn(apiClient.sharedMounts, 'downloadUrl').mockResolvedValue('http://x/download/token')
    const downloadSpy = vi.spyOn(apiClient.sharedMounts, 'download')
    const { getAnchor, clickSpy } = spyOnAppendChild()

    const { result } = renderHook(() => useMountActions(vi.fn(), vi.fn()))
    await act(async () => { await result.current.downloadMountFile(mount({ name: 'Report.pdf' })) })

    expect(urlSpy).toHaveBeenCalledWith('m1')
    expect(getAnchor()?.href).toBe('http://x/download/token')
    expect(getAnchor()?.download).toBe('Report.pdf')
    expect(clickSpy).toHaveBeenCalled()
    expect(downloadSpy).not.toHaveBeenCalled()
  })

  it('reports an error when the token request throws', async () => {
    vi.spyOn(apiClient.sharedMounts, 'downloadUrl').mockRejectedValue(new Error('network'))
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const setError = vi.fn()

    const { result } = renderHook(() => useMountActions(setError, vi.fn()))
    await act(async () => { await result.current.downloadMountFile(mount()) })

    expect(setError).toHaveBeenCalledWith('files.downloadFailed')
  })
})

describe('useMountActions — downloadMountZip', () => {
  it('builds a .zip anchor from the download URL', async () => {
    vi.spyOn(apiClient.sharedMounts, 'downloadUrl').mockResolvedValue('http://x/download/token')
    const { getAnchor, clickSpy } = spyOnAppendChild()

    const { result } = renderHook(() => useMountActions(vi.fn(), vi.fn()))
    await act(async () => { await result.current.downloadMountZip(mount({ name: 'Photos' })) })

    expect(getAnchor()?.download).toBe('Photos.zip')
    expect(clickSpy).toHaveBeenCalled()
  })

  it('reports an error when the zip URL request fails', async () => {
    vi.spyOn(apiClient.sharedMounts, 'downloadUrl').mockRejectedValue(new Error('fail'))
    const setError = vi.fn()

    const { result } = renderHook(() => useMountActions(setError, vi.fn()))
    await act(async () => { await result.current.downloadMountZip(mount()) })

    expect(setError).toHaveBeenCalledWith('files.downloadFailed')
  })
})

describe('useMountActions — leave mount', () => {
  it('does nothing when no leave target is set', async () => {
    const leaveSpy = vi.spyOn(apiClient.sharedMounts, 'leave')
    const { result } = renderHook(() => useMountActions(vi.fn(), vi.fn()))
    await act(async () => { await result.current.handleLeaveMount() })
    expect(leaveSpy).not.toHaveBeenCalled()
  })

  it('leaves the mount and notifies the caller on success', async () => {
    vi.spyOn(apiClient.sharedMounts, 'leave').mockResolvedValue(undefined as never)
    const onMountLeft = vi.fn()
    const { result } = renderHook(() => useMountActions(vi.fn(), onMountLeft))

    act(() => { result.current.setMountLeaveTarget(mount({ id: 'm2' })) })
    await waitFor(() => expect(result.current.mountLeaveTarget?.id).toBe('m2'))

    await act(async () => { await result.current.handleLeaveMount() })

    expect(onMountLeft).toHaveBeenCalledWith('m2')
    expect(result.current.mountLeaveTarget).toBeNull()
    expect(result.current.mountLeaveLoading).toBe(false)
  })

  it('sets an error and keeps loading false when leaving fails', async () => {
    vi.spyOn(apiClient.sharedMounts, 'leave').mockRejectedValue({ body: { error: 'nope' } })
    const { result } = renderHook(() => useMountActions(vi.fn(), vi.fn()))

    act(() => { result.current.setMountLeaveTarget(mount({ id: 'm3' })) })
    await waitFor(() => expect(result.current.mountLeaveTarget?.id).toBe('m3'))

    await act(async () => { await result.current.handleLeaveMount() })

    expect(result.current.mountLeaveError).toBe('nope')
    expect(result.current.mountLeaveLoading).toBe(false)
  })

  it('falls back to the generic error message when the API error has no body', async () => {
    vi.spyOn(apiClient.sharedMounts, 'leave').mockRejectedValue(new Error('boom'))
    const { result } = renderHook(() => useMountActions(vi.fn(), vi.fn()))

    act(() => { result.current.setMountLeaveTarget(mount({ id: 'm4' })) })
    await waitFor(() => expect(result.current.mountLeaveTarget?.id).toBe('m4'))

    await act(async () => { await result.current.handleLeaveMount() })

    expect(result.current.mountLeaveError).toBe('files.deleteRootFailed')
  })
})
