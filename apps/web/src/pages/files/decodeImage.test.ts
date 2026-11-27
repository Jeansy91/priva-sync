import { describe, it, expect } from 'vitest'
import { decodeImageBlob, needsClientDecode } from './decodeImage'

describe('needsClientDecode', () => {
  it.each(['photo.heic', 'photo.HEIC', 'photo.heif', 'scan.tif', 'scan.TIFF'])(
    'ist true fuer %s',
    (name) => {
      expect(needsClientDecode(name)).toBe(true)
    },
  )

  it.each(['photo.png', 'photo.jpg', 'no-extension'])(
    'ist false fuer %s',
    (name) => {
      expect(needsClientDecode(name)).toBe(false)
    },
  )
})

describe('decodeImageBlob', () => {
  it('reicht Blobs mit nativ unterstuetzten Formaten unveraendert durch', async () => {
    const blob = new Blob(['x'], { type: 'image/png' })
    const result = await decodeImageBlob(blob, 'photo.png')
    expect(result).toBe(blob)
  })

  it('reicht Blobs ohne Extension unveraendert durch', async () => {
    const blob = new Blob(['x'], { type: 'application/octet-stream' })
    const result = await decodeImageBlob(blob, 'noext')
    expect(result).toBe(blob)
  })
})
