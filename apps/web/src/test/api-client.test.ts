import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ApiClient, type RefreshResult } from '@privasync/api-client'

describe('ApiClient — Antworten ohne Body', () => {
  let fetchSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    fetchSpy = vi.spyOn(globalThis, 'fetch')
  })

  afterEach(() => {
    fetchSpy.mockRestore()
  })

  it('verarbeitet 202 Accepted mit leerem Body, ohne Fehler zu werfen', async () => {
    fetchSpy.mockResolvedValue(
      new Response(null, { status: 202, statusText: 'Accepted' }),
    )
    const client = new ApiClient('http://localhost')
    await expect(client.auth.requestEmailChange('neu@example.com')).resolves.toBeUndefined()
  })

  it('verlaesst einen Shared-Mount via DELETE /shared-mounts/{id}', async () => {
    fetchSpy.mockResolvedValue(
      new Response(null, { status: 204, statusText: 'No Content' }),
    )
    const client = new ApiClient('http://localhost')
    await expect(client.sharedMounts.leave('mnt-1')).resolves.toBeUndefined()
    const url = fetchSpy.mock.calls[0][0] as string
    const init = fetchSpy.mock.calls[0][1] as RequestInit
    expect(url).toContain('/api/v1/shared-mounts/mnt-1')
    expect(url).not.toContain('/files')
    expect(init.method).toBe('DELETE')
  })

  // Regression: with the Vite dev proxy baseURL is '' (empty). The Shared-Mount
  // thumbnail/download endpoints built their URL via `new URL(baseURL + path)`,
  // which throws "Invalid URL" on a relative path → the call threw, the caller's
  // try/catch swallowed it, and no request ever fired (broken file-mount preview
  // in dev). They must resolve against window.location.origin like the rest.
  it('baut Shared-Mount-Thumbnail-/Download-URLs auch bei leerer baseURL (Dev-Proxy)', async () => {
    fetchSpy.mockResolvedValue({ ok: true, status: 200 } as unknown as Response)
    const client = new ApiClient('') // dev proxy: empty base

    await expect(
      client.sharedMounts.thumbnail('mnt-1', '/', 256, 'contain'),
    ).resolves.toBeTruthy()
    await expect(
      client.sharedMounts.download('mnt-1', '/'),
    ).resolves.toBeTruthy()

    const thumbURL = fetchSpy.mock.calls[0][0] as string
    const dlURL = fetchSpy.mock.calls[1][0] as string
    // Absolute URL (origin-prefixed) and correct mount endpoint + sub-path.
    expect(thumbURL).toMatch(/^https?:\/\/[^/]+\/api\/v1\/shared-mounts\/mnt-1\/thumbnail\?/)
    expect(thumbURL).toContain('path=%2F')
    expect(dlURL).toMatch(/^https?:\/\/[^/]+\/api\/v1\/shared-mounts\/mnt-1\/download\?/)
    expect(dlURL).toContain('path=%2F')
  })

  it('schlaegt /auth/refresh sauber fehl, ohne refreshFn rekursiv aufzurufen', async () => {
    fetchSpy.mockResolvedValue(
      new Response('{"error":"unauthorized"}', { status: 401, statusText: 'Unauthorized' }),
    )
    const client = new ApiClient('http://localhost')
    const refreshFn = vi.fn(async (): Promise<RefreshResult> => {
      try {
        const r = await client.auth.refresh()
        return r.access_token ? { status: 'refreshed', accessToken: r.access_token } : { status: 'unauthorized' }
      } catch {
        return { status: 'unauthorized' }
      }
    })
    client.setRefreshFn(refreshFn)
    const onUnauthorized = vi.fn()
    client.setOnUnauthorized(onUnauthorized)

    await expect(client.auth.refresh()).rejects.toMatchObject({ status: 401 })

    // Genau ein refresh-Request, kein rekursiver Loop ueber refreshFn.
    expect(fetchSpy).toHaveBeenCalledTimes(1)
    expect(refreshFn).not.toHaveBeenCalled()
  })
})

// Office-Vorschau (ADR-0027): beide Endpunkte liefern JSON (Config + Server-URL),
// keine Bytes — daher ueber den normalen request<T>()-Pfad, nicht fetch()+Blob.
describe('ApiClient — officeConfig (ADR-0027)', () => {
  let fetchSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    fetchSpy = vi.spyOn(globalThis, 'fetch')
  })

  afterEach(() => {
    fetchSpy.mockRestore()
  })

  it('ruft den Sync-Root-Endpunkt /sync-roots/{id}/files/office-config auf', async () => {
    fetchSpy.mockResolvedValue(
      new Response(JSON.stringify({ document_server_url: 'https://office.example.com', config: { foo: 'bar' } }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }),
    )
    const client = new ApiClient('http://localhost')
    const res = await client.files.officeConfig('root-1', '/report.docx')
    expect(res.document_server_url).toBe('https://office.example.com')
    expect(res.config).toEqual({ foo: 'bar' })
    const url = fetchSpy.mock.calls[0][0] as string
    expect(url).toContain('/api/v1/sync-roots/root-1/files/office-config')
    expect(url).toContain('path=%2Freport.docx')
  })

  it('ruft den Shared-Mount-Endpunkt /shared-mounts/{id}/files/office-config auf', async () => {
    fetchSpy.mockResolvedValue(
      new Response(JSON.stringify({ document_server_url: 'https://office.example.com', config: {} }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      }),
    )
    const client = new ApiClient('http://localhost')
    await client.sharedMounts.officeConfig('mnt-1', '/sheet.xlsx')
    const url = fetchSpy.mock.calls[0][0] as string
    expect(url).toContain('/api/v1/shared-mounts/mnt-1/files/office-config')
    expect(url).toContain('path=%2Fsheet.xlsx')
  })

  it('wirft ApiRequestError bei 404 (Document-Server nicht konfiguriert)', async () => {
    fetchSpy.mockResolvedValue(
      new Response(JSON.stringify({ error: 'not found' }), { status: 404 }),
    )
    const client = new ApiClient('http://localhost')
    await expect(client.files.officeConfig('root-1', '/report.docx')).rejects.toMatchObject({ status: 404 })
  })
})
