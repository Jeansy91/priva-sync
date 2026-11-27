import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { HttpClient } from './http'
import { ApiRequestError } from '../types'

/**
 * Session recovery on 401 (ADR-0046). Rotation retires the refresh token, so
 * a page that fires N requests in parallel must still refresh only once —
 * otherwise N-1 refreshes race with a retired token and the session dies for
 * no reason.
 */
describe('HttpClient — 401-Recovery', () => {
  let fetchSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => { fetchSpy = vi.spyOn(globalThis, 'fetch') })
  afterEach(() => { fetchSpy.mockRestore() })

  const unauthorized = () =>
    new Response('{"error":"unauthorized"}', { status: 401, statusText: 'Unauthorized' })
  const ok = (body: unknown = { ok: true }) =>
    new Response(JSON.stringify(body), { status: 200 })

  it('refresht genau einmal, wenn mehrere Requests parallel 401 bekommen', async () => {
    // Every request 401s while the old token is set; succeeds once refreshed.
    const client = new HttpClient('http://localhost')
    client.setAccessToken('old')
    fetchSpy.mockImplementation(async (_url: unknown, init?: RequestInit) => {
      const headers = init?.headers as Record<string, string>
      return headers['Authorization'] === 'Bearer new' ? ok({ n: 1 }) : unauthorized()
    })

    let resolveRefresh!: () => void
    const gate = new Promise<void>((r) => { resolveRefresh = r })
    const refreshFn = vi.fn(async () => {
      await gate
      return { status: 'refreshed', accessToken: 'new' } as const
    })
    client.setRefreshFn(refreshFn)
    const onUnauthorized = vi.fn()
    client.setOnUnauthorized(onUnauthorized)

    const all = Promise.all([
      client.request('GET', '/a'),
      client.request('GET', '/b'),
      client.request('GET', '/c'),
    ])
    // Let all three hit their 401 before the refresh resolves.
    await Promise.resolve()
    resolveRefresh()

    await expect(all).resolves.toEqual([{ n: 1 }, { n: 1 }, { n: 1 }])
    expect(refreshFn).toHaveBeenCalledTimes(1)
    expect(onUnauthorized).not.toHaveBeenCalled()
    expect(client.accessToken).toBe('new')
  })

  it('retryt ohne erneuten Refresh, wenn das Token inzwischen schon rotiert ist', async () => {
    const client = new HttpClient('http://localhost')
    client.setAccessToken('old')
    fetchSpy.mockImplementation(async (_url: unknown, init?: RequestInit) => {
      const headers = init?.headers as Record<string, string>
      return headers['Authorization'] === 'Bearer new' ? ok() : unauthorized()
    })
    const refreshFn = vi.fn(async () => ({ status: 'unauthorized' }) as const)
    client.setRefreshFn(refreshFn)

    const pending = client.request('GET', '/a')
    // A different code path refreshed while this request was in flight.
    client.setAccessToken('new')

    await expect(pending).resolves.toEqual({ ok: true })
    expect(refreshFn).not.toHaveBeenCalled()
  })

  it('beendet die Session nur bei typisiertem Unauthorized', async () => {
    const client = new HttpClient('http://localhost')
    client.setAccessToken('old')
    fetchSpy.mockResolvedValue(unauthorized())
    client.setRefreshFn(async () => ({ status: 'unauthorized' }) as const)
    const onUnauthorized = vi.fn()
    client.setOnUnauthorized(onUnauthorized)

    await expect(client.request('GET', '/a')).rejects.toBeInstanceOf(ApiRequestError)
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
  })

  it('haelt die Session bei Transport-/5xx-Fehlern des Refresh am Leben', async () => {
    const client = new HttpClient('http://localhost')
    client.setAccessToken('old')
    fetchSpy.mockResolvedValue(unauthorized())
    client.setRefreshFn(async () => ({ status: 'unavailable' }) as const)
    const onUnauthorized = vi.fn()
    client.setOnUnauthorized(onUnauthorized)

    await expect(client.request('GET', '/a')).rejects.toMatchObject({ status: 401 })
    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  it('wertet einen werfenden refreshFn als voruebergehend, nicht als Logout', async () => {
    const client = new HttpClient('http://localhost')
    client.setAccessToken('old')
    fetchSpy.mockResolvedValue(unauthorized())
    client.setRefreshFn(async () => { throw new TypeError('Failed to fetch') })
    const onUnauthorized = vi.fn()
    client.setOnUnauthorized(onUnauthorized)

    await expect(client.request('GET', '/a')).rejects.toMatchObject({ status: 401 })
    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  it('laesst einen spaeteren 401 erneut refreshen (Single-Flight endet mit dem Refresh)', async () => {
    const client = new HttpClient('http://localhost')
    client.setAccessToken('t0')
    let issued = 0
    fetchSpy.mockImplementation(async (_url: unknown, init?: RequestInit) => {
      const headers = init?.headers as Record<string, string>
      return headers['Authorization'] === `Bearer t${issued}` ? ok() : unauthorized()
    })
    const refreshFn = vi.fn(async () => {
      issued += 1
      return { status: 'refreshed', accessToken: `t${issued}` } as const
    })
    client.setRefreshFn(refreshFn)

    client.setAccessToken('stale')
    await client.request('GET', '/a')
    client.setAccessToken('stale-again')
    await client.request('GET', '/b')

    expect(refreshFn).toHaveBeenCalledTimes(2)
  })
})
