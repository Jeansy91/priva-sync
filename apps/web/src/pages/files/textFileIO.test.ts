import { describe, it, expect, vi, beforeEach } from 'vitest'
import { loadText, saveText } from './textFileIO'
import { apiClient } from '../../store/auth'

vi.mock('../../store/auth', () => ({
  apiClient: {
    files:        { download: vi.fn(), saveText: vi.fn() },
    sharedMounts: { download: vi.fn(), saveText: vi.fn() },
  },
}))

const files = apiClient.files as unknown as { download: ReturnType<typeof vi.fn>; saveText: ReturnType<typeof vi.fn> }
const mounts = apiClient.sharedMounts as unknown as { download: ReturnType<typeof vi.fn>; saveText: ReturnType<typeof vi.fn> }

beforeEach(() => {
  vi.clearAllMocks()
})

describe('loadText', () => {
  it('liest Inhalt und server_version aus dem Download (Sync-Root)', async () => {
    files.download.mockResolvedValue(
      new Response('hello world', {
        status: 200,
        headers: { 'X-Server-Version': '7', 'X-Content-Hash': 'deadbeef' },
      }),
    )
    const got = await loadText({ kind: 'root', rootID: 'r1' }, '/a.txt')
    expect(files.download).toHaveBeenCalledWith('r1', '/a.txt')
    expect(got).toEqual({ content: 'hello world', version: 7, hash: 'deadbeef' })
  })

  it('liefert version null wenn Header fehlt (aelterer Server)', async () => {
    files.download.mockResolvedValue(new Response('x', { status: 200 }))
    const got = await loadText({ kind: 'root', rootID: 'r1' }, '/a.txt')
    expect(got.version).toBeNull()
  })

  it('nutzt den Mount-Transport fuer Mount-Quellen', async () => {
    mounts.download.mockResolvedValue(
      new Response('m', { status: 200, headers: { 'X-Server-Version': '2' } }),
    )
    const got = await loadText({ kind: 'mount', mountID: 'm1' }, '/a.txt')
    expect(mounts.download).toHaveBeenCalledWith('m1', '/a.txt')
    expect(got.version).toBe(2)
  })

  it('wirft bei nicht-ok Download', async () => {
    files.download.mockResolvedValue(new Response('', { status: 404 }))
    await expect(loadText({ kind: 'root', rootID: 'r1' }, '/a.txt')).rejects.toThrow()
  })
})

describe('saveText', () => {
  it('routet zum Sync-Root-saveText mit If-Match', async () => {
    files.saveText.mockResolvedValue({ status: 'ok', version: 8, contentHash: 'h' })
    const res = await saveText({ kind: 'root', rootID: 'r1' }, '/a.txt', 'new', { ifMatch: 7 })
    expect(files.saveText).toHaveBeenCalledWith('r1', '/a.txt', 'new', { ifMatch: 7 })
    expect(res).toEqual({ status: 'ok', version: 8, contentHash: 'h' })
  })

  it('reicht einen Conflict unveraendert durch', async () => {
    mounts.saveText.mockResolvedValue({ status: 'conflict', serverVersion: 9, serverHash: 'sh' })
    const res = await saveText({ kind: 'mount', mountID: 'm1' }, '/a.txt', 'new', { ifMatch: 5 })
    expect(mounts.saveText).toHaveBeenCalledWith('m1', '/a.txt', 'new', { ifMatch: 5 })
    expect(res).toEqual({ status: 'conflict', serverVersion: 9, serverHash: 'sh' })
  })
})
