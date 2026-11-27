import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { apiClient } from '../../store/auth'
import { useMediaStreamSrc } from './useMediaStreamSrc'
import type { FileSource } from './fileSource'

const rootSource: FileSource = { kind: 'root', rootID: 'r-1' }

beforeEach(() => {
  vi.restoreAllMocks()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useMediaStreamSrc', () => {
  it('mints a token and exposes the resulting src', async () => {
    vi.spyOn(apiClient.files, 'mediaStreamUrl').mockResolvedValue({
      url: 'https://api.test/media/tok-1', expiresIn: 900,
    })

    const { result } = renderHook(() => useMediaStreamSrc(rootSource, '/clip.mp4'))

    await waitFor(() => expect(result.current.src).toBe('https://api.test/media/tok-1'))
    expect(result.current.error).toBe(false)
    expect(apiClient.files.mediaStreamUrl).toHaveBeenCalledWith('r-1', '/clip.mp4')
  })

  it('returns no src without a path', () => {
    const spy = vi.spyOn(apiClient.files, 'mediaStreamUrl')
    const { result } = renderHook(() => useMediaStreamSrc(rootSource, null))
    expect(result.current).toEqual({ src: null, error: false })
    expect(spy).not.toHaveBeenCalled()
  })

  it('surfaces an error when the initial mint fails', async () => {
    vi.spyOn(apiClient.files, 'mediaStreamUrl').mockRejectedValue(new Error('boom'))

    const { result } = renderHook(() => useMediaStreamSrc(rootSource, '/clip.mp4'))

    await waitFor(() => expect(result.current.error).toBe(true))
    expect(result.current.src).toBeNull()
  })

  it('renews the grant near expiry and exposes the new src', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout'] })
    const spy = vi.spyOn(apiClient.files, 'mediaStreamUrl')
      .mockResolvedValueOnce({ url: 'https://api.test/media/tok-1', expiresIn: 1000 })
      .mockResolvedValueOnce({ url: 'https://api.test/media/tok-2', expiresIn: 1000 })

    const { result } = renderHook(() => useMediaStreamSrc(rootSource, '/clip.mp4'))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(result.current.src).toBe('https://api.test/media/tok-1')

    // 75% of a 1000s TTL is 750s — short of that, no renewal yet.
    await act(async () => { await vi.advanceTimersByTimeAsync(700 * 1000) })
    expect(result.current.src).toBe('https://api.test/media/tok-1')

    await act(async () => { await vi.advanceTimersByTimeAsync(100 * 1000) })
    expect(spy).toHaveBeenCalledTimes(2)
    expect(result.current.src).toBe('https://api.test/media/tok-2')
  })

  it('keeps the current src when a background renewal fails, and retries', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout'] })
    const spy = vi.spyOn(apiClient.files, 'mediaStreamUrl')
      .mockResolvedValueOnce({ url: 'https://api.test/media/tok-1', expiresIn: 1000 })
      .mockRejectedValueOnce(new Error('transient'))
      .mockResolvedValueOnce({ url: 'https://api.test/media/tok-2', expiresIn: 1000 })

    const { result } = renderHook(() => useMediaStreamSrc(rootSource, '/clip.mp4'))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(result.current.src).toBe('https://api.test/media/tok-1')

    // Renewal fires at 750s and fails — the still-working src must survive.
    await act(async () => { await vi.advanceTimersByTimeAsync(750 * 1000) })
    expect(result.current).toEqual({ src: 'https://api.test/media/tok-1', error: false })

    // Retry (30s later) succeeds.
    await act(async () => { await vi.advanceTimersByTimeAsync(30 * 1000) })
    expect(spy).toHaveBeenCalledTimes(3)
    expect(result.current.src).toBe('https://api.test/media/tok-2')
  })
})
