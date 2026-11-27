import { apiClient } from '../../store/auth'
import { previewKindFor, type PreviewKind } from './previewKind'
import { decodeImageBlob, needsClientDecode } from './decodeImage'
import { extractCover } from './id3Cover'

const PREVIEW_SIZE = 256
const PREVIEW_FIT: 'contain' = 'contain'

// Kinds, deren Cover wir clientseitig aus dem Container extrahieren —
// größere Downloads, deshalb getrennt von den Server-Thumbnails behandelt.
const CLIENT_KINDS: PreviewKind[] = ['audio', 'comic', 'epub']

// PreviewIO abstracts the two transports a preview can come from — the
// sync-root endpoint or the Shared-Mount endpoint — so the kind-dispatch
// logic below is written once. thumbnail() is pre-sized to PREVIEW_SIZE.
interface PreviewIO {
  thumbnail: () => Promise<Response>
  download:  () => Promise<Response>
}

function rootIO(rootID: string, path: string): PreviewIO {
  return {
    thumbnail: () => apiClient.files.thumbnail(rootID, path, PREVIEW_SIZE, PREVIEW_FIT),
    download:  () => apiClient.files.download(rootID, path),
  }
}

// `path` is the mount-relative sub-path. For a file-mount it is "/" (the mount
// root is the file itself); for a file browsed inside a dir-mount it is that
// file's own path. See ADR-0010 and thumbHostFor in helpers.ts.
function mountIO(mountID: string, path: string): PreviewIO {
  return {
    thumbnail: () => apiClient.sharedMounts.thumbnail(mountID, path, PREVIEW_SIZE, PREVIEW_FIT),
    download:  () => apiClient.sharedMounts.download(mountID, path),
  }
}

// fetchPreviewBlob liefert ein Bild-Blob für die Detail-Vorschau oder null,
// wenn keine sinnvolle Vorschau möglich ist (Caller fällt auf Icon zurück).
export function fetchPreviewBlob(rootID: string, path: string, name: string): Promise<Blob | null> {
  return fetchPreviewBlobVia(rootIO(rootID, path), name)
}

// Mount-Variante: lädt die Vorschau einer geteilten Datei über den
// Shared-Mount-Endpoint. `path` ist der mount-relative Sub-Pfad ("/" für
// Einzeldatei-Mounts, der Dateipfad für Dateien in einem Verzeichnis-Mount).
export function fetchMountPreviewBlob(mountID: string, path: string, name: string): Promise<Blob | null> {
  return fetchPreviewBlobVia(mountIO(mountID, path), name)
}

async function fetchPreviewBlobVia(io: PreviewIO, name: string): Promise<Blob | null> {
  const kind = previewKindFor(name)
  if (!kind) return null

  if (kind === 'image' || kind === 'video' || kind === 'pdf') {
    return serverThumbVia(io, name)
  }
  // Mesh-Formate (STL/OBJ/PLY) und Slicer-3MF-Container rendert der Server.
  if (kind === 'model3d' && hasServerMeshThumb(name)) {
    return serverThumbVia(io, name)
  }
  // Comics: zuerst Server (CBZ via stdlib, CBR via unrar). Wenn der Server
  // nicht ausliefern kann (kein unrar, kaputtes Archiv), Client-Fallback.
  if (kind === 'comic') {
    const fromServer = await serverThumbVia(io, name)
    if (fromServer) return fromServer
  }
  if (!CLIENT_KINDS.includes(kind)) return null

  try {
    const res = await io.download()
    if (!res.ok) return null
    const blob = await res.blob()

    if (kind === 'audio') {
      const cover = await extractCover(blob)
      return cover?.blob ?? null
    }
    if (kind === 'comic') return firstZipImage(blob, name)
    if (kind === 'epub')  return epubCover(blob)
  } catch {
    return null
  }
  return null
}

async function serverThumbVia(io: PreviewIO, name: string): Promise<Blob | null> {
  try {
    const res = await io.thumbnail()
    if (res.ok) return await res.blob()
    if (needsClientDecode(name)) {
      const raw = await io.download()
      if (!raw.ok) return null
      return await decodeImageBlob(await raw.blob(), name)
    }
    return null
  } catch {
    return null
  }
}

const IMG_EXT_RE = /\.(jpe?g|png|gif|webp|avif|bmp)$/i
const SERVER_MESH_EXT_RE = /\.(stl|obj|ply|3mf)$/i

function hasServerMeshThumb(name: string): boolean {
  return SERVER_MESH_EXT_RE.test(name)
}

async function firstZipImage(blob: Blob, name: string): Promise<Blob | null> {
  // CBR (Rar): heavy lazy-load. Fallback auf Icon, wenn das Modul fehlt.
  if (/\.cbr$/i.test(name)) return firstRarImage(blob)
  return firstZipImageEntry(blob)
}

async function firstZipImageEntry(blob: Blob): Promise<Blob | null> {
  const mod: any = await import('jszip')
  const JSZip = mod.default || mod
  const zip = await JSZip.loadAsync(await blob.arrayBuffer())
  const names: string[] = []
  zip.forEach((p: string, f: any) => {
    if (f.dir) return
    if (IMG_EXT_RE.test(p)) names.push(p)
  })
  if (names.length === 0) return null
  names.sort((a, b) => a.localeCompare(b, undefined, { numeric: true }))
  const buf: ArrayBuffer = await zip.file(names[0]).async('arraybuffer')
  return new Blob([buf], { type: mimeForImageExt(names[0]) })
}

async function firstRarImage(blob: Blob): Promise<Blob | null> {
  try {
    const mod: any = await import('node-unrar-js')
    const buf = new Uint8Array(await blob.arrayBuffer())
    const extractor = await mod.createExtractorFromData({ data: buf })
    const names: string[] = []
    for (const h of extractor.getFileList().fileHeaders as Iterable<any>) {
      if (!h.flags?.directory && IMG_EXT_RE.test(h.name)) names.push(h.name)
    }
    if (names.length === 0) return null
    names.sort((a, b) => a.localeCompare(b, undefined, { numeric: true }))
    const result = extractor.extract({ files: [names[0]] })
    for (const f of result.files as Iterable<any>) {
      if (f.extraction) {
        const u8 = f.extraction as Uint8Array
        const copy = new Uint8Array(u8)
        return new Blob([copy.buffer], { type: mimeForImageExt(names[0]) })
      }
    }
  } catch {
    return null
  }
  return null
}

async function epubCover(blob: Blob): Promise<Blob | null> {
  // Minimalstrategie: erstes Bild im Archiv. Vollständiges OPF-Parsing wäre
  // schöner, ist für die Detail-Vorschau aber Overkill.
  return firstZipImageEntry(blob)
}

function mimeForImageExt(name: string): string {
  const m = name.toLowerCase().match(/\.([a-z0-9]+)$/)
  switch (m?.[1]) {
    case 'jpg':
    case 'jpeg': return 'image/jpeg'
    case 'png':  return 'image/png'
    case 'gif':  return 'image/gif'
    case 'webp': return 'image/webp'
    case 'avif': return 'image/avif'
    case 'bmp':  return 'image/bmp'
    default:     return 'application/octet-stream'
  }
}
