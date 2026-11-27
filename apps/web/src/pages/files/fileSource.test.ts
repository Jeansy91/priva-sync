import { describe, it, expect, beforeEach, vi } from 'vitest'
import { apiClient } from '../../store/auth'
import { downloadFrom, thumbnailFrom, archiveListFrom, type FileSource } from './fileSource'

const root: FileSource  = { kind: 'root',  rootID: 'r-1' }
const mount: FileSource = { kind: 'mount', mountID: 'm-1' }

beforeEach(() => { vi.restoreAllMocks() })

describe('downloadFrom', () => {
  it('Sync-Root → files.download', () => {
    const spy = vi.spyOn(apiClient.files, 'download').mockResolvedValue({} as any)
    downloadFrom(root, '/a.png')
    expect(spy).toHaveBeenCalledWith('r-1', '/a.png')
  })

  it('Mount → sharedMounts.download', () => {
    const spy = vi.spyOn(apiClient.sharedMounts, 'download').mockResolvedValue({} as any)
    downloadFrom(mount, '/')
    expect(spy).toHaveBeenCalledWith('m-1', '/')
  })
})

describe('thumbnailFrom', () => {
  it('Sync-Root → files.thumbnail', () => {
    const spy = vi.spyOn(apiClient.files, 'thumbnail').mockResolvedValue({} as any)
    thumbnailFrom(root, '/a.png', 128, 'contain')
    expect(spy).toHaveBeenCalledWith('r-1', '/a.png', 128, 'contain')
  })

  it('Mount → sharedMounts.thumbnail', () => {
    const spy = vi.spyOn(apiClient.sharedMounts, 'thumbnail').mockResolvedValue({} as any)
    thumbnailFrom(mount, '/', 128, 'contain')
    expect(spy).toHaveBeenCalledWith('m-1', '/', 128, 'contain')
  })
})

describe('archiveListFrom', () => {
  it('Sync-Root → files.archiveList', () => {
    const spy = vi.spyOn(apiClient.files, 'archiveList').mockResolvedValue({ entries: [] } as any)
    archiveListFrom(root, '/a.zip')
    expect(spy).toHaveBeenCalledWith('r-1', '/a.zip')
  })

  it('Mount → sharedMounts.archiveList', () => {
    const spy = vi.spyOn(apiClient.sharedMounts, 'archiveList').mockResolvedValue({ entries: [] } as any)
    archiveListFrom(mount, '/')
    expect(spy).toHaveBeenCalledWith('m-1', '/')
  })
})
