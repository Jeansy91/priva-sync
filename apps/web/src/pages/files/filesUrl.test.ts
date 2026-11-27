import { describe, it, expect } from 'vitest'
import { parseFilesUrl } from './filesUrl'

describe('parseFilesUrl', () => {
  it('treats empty splat as the root listing', () => {
    expect(parseFilesUrl('')).toMatchObject({
      mountMode: false, urlRootName: null, urlPath: '/',
    })
  })

  it('extracts a single root name as the active own-root', () => {
    expect(parseFilesUrl('Photos')).toMatchObject({
      mountMode: false, urlRootName: 'Photos', urlPath: '/',
    })
  })

  it('joins sub-path segments under an own root', () => {
    expect(parseFilesUrl('Photos/2024/holiday')).toMatchObject({
      mountMode: false, urlRootName: 'Photos', urlPath: '/2024/holiday',
    })
  })

  it('decodes URL-encoded segments', () => {
    expect(parseFilesUrl('Vacation%20Photos/N%C3%BCrnberg')).toMatchObject({
      urlRootName: 'Vacation Photos',
      urlPath:     '/Nürnberg',
    })
  })

  it('recognises mount mode and extracts the share id', () => {
    expect(parseFilesUrl('_m/abc-123')).toMatchObject({
      mountMode: true, mountIDParam: 'abc-123', urlRootName: null,
      mountSubPath: '/', urlPath: '/',
    })
  })

  it('extracts the sub-path inside a mount', () => {
    expect(parseFilesUrl('_m/abc-123/sub/dir')).toMatchObject({
      mountMode: true, mountIDParam: 'abc-123',
      mountSubPath: '/sub/dir', urlPath: '/sub/dir',
    })
  })
})
