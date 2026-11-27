import { useEffect, useRef } from 'react'
import { EnvelopeVersion } from '@privasync/api-client'
import { apiClient, useAuthStore } from '../store/auth'

type SSEHandler = (data: unknown) => void

// unwrapEnvelope validates the {type, version, data} envelope and returns
// envelope.data. Events with a mismatched version are dropped so a stale
// client cannot misinterpret a re-shaped payload. Exported for unit testing.
export function unwrapEnvelope(raw: string): unknown | undefined {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return undefined
  }
  if (typeof parsed !== 'object' || parsed === null) return undefined
  const env = parsed as { version?: unknown; data?: unknown }
  if (env.version !== EnvelopeVersion) {
    console.warn(
      `[sse] dropping event with envelope version ${String(env.version)}, expected ${EnvelopeVersion}`,
    )
    return undefined
  }
  return env.data
}

/**
 * Opens a persistent SSE connection to /api/v1/events.
 * Calls the provided handlers when matching event types arrive.
 * Auto-reconnects on disconnect (browser EventSource handles this natively).
 *
 * `onResync` is invoked only when the connection was actually dropped and
 * reopened — events may have been missed in that window. Pure tab focus or
 * visibility changes do NOT trigger a resync, since EventSource keeps the
 * stream alive in background tabs. The `online` event still triggers a
 * resync because the underlying socket is guaranteed to have been torn down.
 */
export function useSSE(handlers: Record<string, SSEHandler>, onResync?: () => void) {
  const { isAuthenticated } = useAuthStore()
  const handlersRef = useRef(handlers)
  handlersRef.current = handlers
  const onResyncRef = useRef(onResync)
  onResyncRef.current = onResync

  useEffect(() => {
    if (!isAuthenticated) return

    const source = apiClient.openEventStream()
    let firstOpen = true
    let droppedSinceLastOpen = false

    const onOpen = () => {
      if (firstOpen) {
        firstOpen = false
        return
      }
      // Only resync if we actually saw a drop. A spurious open without a
      // preceding error would mean nothing was missed.
      if (droppedSinceLastOpen) {
        droppedSinceLastOpen = false
        onResyncRef.current?.()
      }
    }
    const onError = () => {
      // EventSource fires 'error' on every disconnect; the browser will then
      // re-attempt. Flag that we missed events so the next 'open' resyncs.
      if (source.readyState !== EventSource.OPEN) {
        droppedSinceLastOpen = true
      }
    }
    source.addEventListener('open',  onOpen)
    source.addEventListener('error', onError)

    const attachedTypes = new Set<string>()
    for (const type of Object.keys(handlersRef.current)) {
      if (attachedTypes.has(type)) continue
      source.addEventListener(type, (e: MessageEvent) => {
        const data = unwrapEnvelope(e.data)
        if (data === undefined) return
        handlersRef.current[type]?.(data)
      })
      attachedTypes.add(type)
    }

    // Network coming back online after an offline pause: the socket is
    // guaranteed dead, force a resync once it reopens.
    const onOnline = () => { droppedSinceLastOpen = true }
    window.addEventListener('online', onOnline)

    return () => {
      source.removeEventListener('open',  onOpen)
      source.removeEventListener('error', onError)
      source.close()
      window.removeEventListener('online', onOnline)
    }
  }, [isAuthenticated])
}
