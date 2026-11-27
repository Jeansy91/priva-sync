import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { unwrapEnvelope, useSSE } from './useSSE'
import { apiClient, useAuthStore } from '../store/auth'
import { EnvelopeVersion } from '@privasync/api-client'

describe('unwrapEnvelope', () => {
  it('returns envelope.data for matching version', () => {
    const raw = JSON.stringify({
      type: 'file_uploaded',
      version: 2,
      data: { root_id: 'r1', path: '/x.md' },
    })
    expect(unwrapEnvelope(raw)).toEqual({ root_id: 'r1', path: '/x.md' })
  })

  it('drops events with a mismatched envelope version', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const raw = JSON.stringify({
      type: 'file_uploaded',
      version: 99,
      data: { root_id: 'r1' },
    })
    expect(unwrapEnvelope(raw)).toBeUndefined()
    expect(warn).toHaveBeenCalledOnce()
    warn.mockRestore()
  })

  it('drops malformed JSON', () => {
    expect(unwrapEnvelope('not json')).toBeUndefined()
  })

  it('drops non-object envelopes', () => {
    expect(unwrapEnvelope('42')).toBeUndefined()
    expect(unwrapEnvelope('null')).toBeUndefined()
  })
})

// FakeEventSource stands in for the browser EventSource — jsdom has no real
// implementation. It only needs to support what useSSE actually touches:
// addEventListener/removeEventListener, readyState/OPEN and close().
class FakeEventSource extends EventTarget {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
  readyState: number = FakeEventSource.OPEN
  closed = false
  close() {
    this.closed = true
    this.readyState = FakeEventSource.CLOSED
  }
}

function envelope(data: unknown, version = EnvelopeVersion) {
  return JSON.stringify({ version, data })
}

describe('useSSE', () => {
  let realEventSource: unknown
  let source: FakeEventSource

  beforeEach(() => {
    realEventSource = (globalThis as any).EventSource
    ;(globalThis as any).EventSource = FakeEventSource
    source = new FakeEventSource()
    vi.spyOn(apiClient, 'openEventStream').mockReturnValue(source as unknown as EventSource)
    useAuthStore.setState({ user: null, accessToken: null, isAuthenticated: true })
  })

  afterEach(() => {
    ;(globalThis as any).EventSource = realEventSource
    vi.restoreAllMocks()
    useAuthStore.setState({ user: null, accessToken: null, isAuthenticated: false })
  })

  it('oeffnet keinen Stream, solange nicht authentifiziert', () => {
    useAuthStore.setState({ isAuthenticated: false })
    renderHook(() => useSSE({}))
    expect(apiClient.openEventStream).not.toHaveBeenCalled()
  })

  it('oeffnet den Stream und ruft den passenden Handler mit entpackten Daten auf', () => {
    const handler = vi.fn()
    renderHook(() => useSSE({ quota_updated: handler }))
    expect(apiClient.openEventStream).toHaveBeenCalledOnce()

    act(() => {
      source.dispatchEvent(
        new MessageEvent('quota_updated', { data: envelope({ used_bytes: 1 }) }),
      )
    })
    expect(handler).toHaveBeenCalledWith({ used_bytes: 1 })
  })

  it('ignoriert Events mit falscher Envelope-Version', () => {
    const handler = vi.fn()
    renderHook(() => useSSE({ quota_updated: handler }))

    act(() => {
      source.dispatchEvent(
        new MessageEvent('quota_updated', { data: envelope({ x: 1 }, 99) }),
      )
    })
    expect(handler).not.toHaveBeenCalled()
  })

  it('resynct nicht beim ersten open, aber danach nach einem Drop', () => {
    const onResync = vi.fn()
    renderHook(() => useSSE({}, onResync))

    act(() => { source.dispatchEvent(new Event('open')) })
    expect(onResync).not.toHaveBeenCalled()

    act(() => {
      source.readyState = FakeEventSource.CONNECTING
      source.dispatchEvent(new Event('error'))
    })
    act(() => {
      source.readyState = FakeEventSource.OPEN
      source.dispatchEvent(new Event('open'))
    })
    expect(onResync).toHaveBeenCalledOnce()
  })

  it('resynct nicht bei einem open ohne vorherigen Drop', () => {
    const onResync = vi.fn()
    renderHook(() => useSSE({}, onResync))

    act(() => { source.dispatchEvent(new Event('open')) })
    act(() => { source.dispatchEvent(new Event('open')) })
    expect(onResync).not.toHaveBeenCalled()
  })

  it('markiert einen Drop, wenn online gefeuert wird, und resynct beim naechsten open', () => {
    const onResync = vi.fn()
    renderHook(() => useSSE({}, onResync))

    act(() => { source.dispatchEvent(new Event('open')) })
    act(() => { window.dispatchEvent(new Event('online')) })
    act(() => { source.dispatchEvent(new Event('open')) })
    expect(onResync).toHaveBeenCalledOnce()
  })

  it('schliesst den Stream beim Unmount', () => {
    const { unmount } = renderHook(() => useSSE({}))
    unmount()
    expect(source.closed).toBe(true)
  })
})
