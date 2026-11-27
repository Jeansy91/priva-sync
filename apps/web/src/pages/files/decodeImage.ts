// Decodiert Formate, die Browser nicht nativ unterstützen (HEIC/HEIF, TIFF),
// zu einem image/png-Blob. Andere Formate werden durchgereicht.

export async function decodeImageBlob(blob: Blob, name: string): Promise<Blob> {
  const ext = name.split('.').pop()?.toLowerCase()
  switch (ext) {
    case 'heic':
    case 'heif':
      return await decodeHEIC(blob)
    case 'tif':
    case 'tiff':
      return await decodeTIFF(blob)
    default:
      return blob
  }
}

async function decodeHEIC(blob: Blob): Promise<Blob> {
  const mod: any = await import('libheif-js')
  const libheif = mod.default || mod
  const decoder = new libheif.HeifDecoder()
  const buf = await blob.arrayBuffer()
  const images = decoder.decode(new Uint8Array(buf))
  if (!images || images.length === 0) throw new Error('heic: no images')
  const img = images[0]
  const w = img.get_width()
  const h = img.get_height()

  const canvas = document.createElement('canvas')
  canvas.width = w
  canvas.height = h
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('heic: no 2d context')
  const imageData = ctx.createImageData(w, h)

  await new Promise<void>((resolve, reject) => {
    img.display(imageData, (displayed: ImageData | null) => {
      if (!displayed) reject(new Error('heic display failed'))
      else resolve()
    })
  })
  ctx.putImageData(imageData, 0, 0)
  return canvasToPng(canvas)
}

async function decodeTIFF(blob: Blob): Promise<Blob> {
  const mod: any = await import('utif')
  const UTIF = mod.default || mod
  const buf = await blob.arrayBuffer()
  const ifds = UTIF.decode(buf)
  if (!ifds || ifds.length === 0) throw new Error('tiff: no IFDs')
  UTIF.decodeImage(buf, ifds[0])
  const rgba = UTIF.toRGBA8(ifds[0])
  const w = ifds[0].width
  const h = ifds[0].height

  const canvas = document.createElement('canvas')
  canvas.width = w
  canvas.height = h
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('tiff: no 2d context')
  ctx.putImageData(new ImageData(new Uint8ClampedArray(rgba), w, h), 0, 0)
  return canvasToPng(canvas)
}

function canvasToPng(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((b) => b ? resolve(b) : reject(new Error('canvas toBlob failed')), 'image/png')
  })
}

export function needsClientDecode(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase()
  return ext === 'heic' || ext === 'heif' || ext === 'tif' || ext === 'tiff'
}
