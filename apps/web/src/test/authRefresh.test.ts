import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

/**
 * The refresh wiring in the auth store (ADR-0046). Two rules it must keep:
 * a fresh access token belongs in the store (localStorage + SSE consumers see
 * it), and only a real 401 from /auth/refresh ends the session.
 */
describe('Auth-Store — Refresh-Verdrahtung', () => {
  let fetchSpy: ReturnType<typeof vi.spyOn>

  beforeEach(async () => {
    vi.resetModules()
    localStorage.clear()
    fetchSpy = vi.spyOn(globalThis, 'fetch')
  })
  afterEach(() => { fetchSpy.mockRestore() })

  async function loadStore() {
    const mod = await import('../store/auth')
    mod.useAuthStore.getState().setTokens('old')
    return mod
  }

  it('schreibt das erneuerte Access-Token in den Store', async () => {
    const { useAuthStore } = await loadStore()
    fetchSpy.mockImplementation(async (url: unknown) => {
      if (String(url).includes('/auth/refresh')) {
        return new Response(JSON.stringify({ access_token: 'fresh', expires_in: 900 }), { status: 200 })
      }
      return new Response('{"error":"unauthorized"}', { status: 401 })
    })

    const { apiClient } = await import('../store/auth')
    await expect(apiClient.auth.me()).rejects.toBeTruthy()

    expect(useAuthStore.getState().accessToken).toBe('fresh')
    expect(useAuthStore.getState().isAuthenticated).toBe(true)
  })

  it('loggt bei 401 auf /auth/refresh aus', async () => {
    const { useAuthStore, apiClient } = await loadStore()
    fetchSpy.mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 }))

    await expect(apiClient.auth.me()).rejects.toBeTruthy()
    expect(useAuthStore.getState().isAuthenticated).toBe(false)
  })

  it('bleibt bei einem 500 auf /auth/refresh eingeloggt', async () => {
    const { useAuthStore, apiClient } = await loadStore()
    fetchSpy.mockImplementation(async (url: unknown) =>
      String(url).includes('/auth/refresh')
        ? new Response('{"error":"internal"}', { status: 500 })
        : new Response('{"error":"unauthorized"}', { status: 401 }),
    )

    await expect(apiClient.auth.me()).rejects.toBeTruthy()
    expect(useAuthStore.getState().isAuthenticated).toBe(true)
  })

  it('bleibt bei einem Netzwerkfehler auf /auth/refresh eingeloggt', async () => {
    const { useAuthStore, apiClient } = await loadStore()
    fetchSpy.mockImplementation(async (url: unknown) => {
      if (String(url).includes('/auth/refresh')) throw new TypeError('Failed to fetch')
      return new Response('{"error":"unauthorized"}', { status: 401 })
    })

    await expect(apiClient.auth.me()).rejects.toBeTruthy()
    expect(useAuthStore.getState().isAuthenticated).toBe(true)
  })
})
