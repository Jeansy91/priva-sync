import { describe, it, expect, vi, afterEach } from 'vitest'
import { ApiClient } from './client'

/**
 * The stream credential must not travel in the URL: it would land in the
 * server's request log, any reverse-proxy log and the browser history.
 * Authentication happens through the scoped sse_token cookie instead, which
 * EventSource sends automatically when withCredentials is set (ADR-0038).
 */
describe('openEventStream', () => {
  const created: Array<{ url: string; init?: EventSourceInit }> = []

  class FakeEventSource {
    constructor(url: string, init?: EventSourceInit) {
      created.push({ url, init })
    }
    close() {}
  }

  afterEach(() => {
    created.length = 0
    vi.unstubAllGlobals()
  })

  function open(accessToken: string | null) {
    vi.stubGlobal('EventSource', FakeEventSource)
    const client = new ApiClient('https://example.test')
    if (accessToken) client.setAccessToken(accessToken)
    client.openEventStream()
    return created[0]
  }

  it('does not put the access token in the URL', () => {
    const { url } = open('super-secret-access-token')
    expect(url).toBe('https://example.test/api/v1/events')
    expect(url).not.toContain('token')
    expect(url).not.toContain('super-secret-access-token')
  })

  it('sends credentials so the cookie is included', () => {
    const { init } = open('super-secret-access-token')
    expect(init?.withCredentials).toBe(true)
  })

  it('builds the same URL when no token is set', () => {
    const { url } = open(null)
    expect(url).toBe('https://example.test/api/v1/events')
  })
})
