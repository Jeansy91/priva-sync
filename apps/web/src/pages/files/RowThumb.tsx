import { useEffect, useRef, useState } from 'react'
import { FileIcon } from '@privasync/ui'
import { apiClient } from '../../store/auth'
import { previewKindFor } from './previewKind'
import { decodeImageBlob, needsClientDecode } from './decodeImage'
import { fetchMountPreviewBlob, fetchPreviewBlob } from './previewBlob'
import { getCachedThumb, setCachedThumb, thumbCacheKey } from './thumbCache'

type ThumbSize = 'sm' | 'lg'

type Props = {
  rootID:   string
  path:     string
  name:     string
  size?:    ThumbSize
  // When set, the thumbnail is fetched via the Shared-Mount endpoint
  // (`sharedMounts.thumbnail(mountID, path, ...)`) instead of the
  // sync-root one. `rootID` still scopes the in-memory cache key so
  // mount thumbs do not collide with owned-root thumbs. See ADR-0010.
  mountID?: string
}

// Pixel size requested from the thumbnail endpoint per variant. Display
// dimensions live in WRAP_CLASS / ICON_CLASS below. `lg` requests 256 px so
// the cache entry is shared with FileDetailPreview (PREVIEW_SIZE).
const FETCH_PX: Record<ThumbSize, number> = { sm: 64, lg: 256 }

const WRAP_CLASS: Record<ThumbSize, string> = {
  sm: 'h-7 w-7',
  lg: 'h-24 w-24',
}
const ICON_CLASS: Record<ThumbSize, string> = {
  sm: 'h-4 w-4',
  lg: 'h-12 w-12',
}

// RowThumb shows a square preview image, falling back to the FileIcon.
// The container is the same size for a given variant so file names line up
// underneath. `sm` (table rows) handles image/video only via the cheap
// server-thumbnail endpoint. `lg` (grid tiles) reuses fetchPreviewBlob from
// the detail panel so PDFs, audio covers, comic covers, etc. also render.
export function RowThumb({ rootID, path, name, size = 'sm', mountID }: Props) {
  const kind = previewKindFor(name)
  const thumbable = size === 'lg' ? kind !== null : (kind === 'image' || kind === 'video')
  const px = FETCH_PX[size]
  const cacheKey = thumbCacheKey(rootID, path, px)
  const wrapRef = useRef<HTMLSpanElement | null>(null)
  const [visible,   setVisible]   = useState(false)
  const [url,       setUrl]       = useState<string | null>(() => thumbable ? getCachedThumb(cacheKey) : null)
  const [failed,    setFailed]    = useState(false)

  useEffect(() => {
    if (!thumbable) return
    const el = wrapRef.current
    if (!el || typeof IntersectionObserver === 'undefined') {
      setVisible(true)
      return
    }
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) { setVisible(true); io.disconnect(); break }
        }
      },
      { rootMargin: '200px' },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [thumbable])

  useEffect(() => {
    if (!thumbable || !visible) return
    const cached = getCachedThumb(cacheKey)
    if (cached) { setUrl(cached); return }

    let cancelled = false
    ;(async () => {
      // `lg` (grid) renders rich covers (PDF/audio/comic/epub) via the
      // preview-blob path; `sm` (table) stays on the cheap image/video thumb.
      // The mount variants mirror the owned-file ones so file-mounts reach
      // full thumbnail parity. See ADR-0010 / ADR-0012.
      const blob = size === 'lg'
        ? (mountID
            ? await fetchMountPreviewBlob(mountID, path, name)
            : await fetchPreviewBlob(rootID, path, name))
        : (mountID
            ? await fetchMountThumb(mountID, path, name, px)
            : await fetchCheapThumb(rootID, path, name, px))
      if (cancelled) return
      if (!blob) { setFailed(true); return }
      const objUrl = URL.createObjectURL(blob)
      setCachedThumb(cacheKey, objUrl)
      setUrl(objUrl)
    })()
    return () => { cancelled = true }
  }, [thumbable, visible, cacheKey, rootID, path, name, px, size, mountID])

  return (
    <span
      ref={wrapRef}
      data-testid="row-thumb"
      className={
        `inline-flex items-center justify-center ${WRAP_CLASS[size]} shrink-0 rounded-sm overflow-hidden `
        + (thumbable && url && !failed ? 'bg-muted/50' : '')
      }
    >
      {thumbable && url && !failed ? (
        <img
          src={url}
          alt={name}
          draggable={false}
          className={`h-full w-full ${size === 'lg' ? 'object-contain' : 'object-cover'}`}
        />
      ) : (
        <FileIcon name={name} className={ICON_CLASS[size]} />
      )}
    </span>
  )
}

// Mount equivalent of fetchCheapThumb. Goes through the Shared-Mount
// thumbnail endpoint so the recipient does not need direct sync-root
// access. Same client-decode fallback for HEIC/TIFF.
async function fetchMountThumb(
  mountID: string,
  path:    string,
  name:    string,
  px:      number,
): Promise<Blob | null> {
  try {
    const res = await apiClient.sharedMounts.thumbnail(mountID, path, px)
    if (res.ok) return await res.blob()
    if (needsClientDecode(name)) {
      const raw = await apiClient.sharedMounts.download(mountID, path)
      if (!raw.ok) return null
      return await decodeImageBlob(await raw.blob(), name)
    }
    return null
  } catch {
    return null
  }
}

// Cheap fast path for table rows: server-side image/video thumbnail only,
// with a HEIC/TIFF client-decode fallback. Returns null on any failure so the
// caller falls back to the FileIcon.
async function fetchCheapThumb(
  rootID: string,
  path:   string,
  name:   string,
  px:     number,
): Promise<Blob | null> {
  try {
    const res = await apiClient.files.thumbnail(rootID, path, px)
    if (res.ok) return await res.blob()
    if (needsClientDecode(name)) {
      const raw = await apiClient.files.download(rootID, path)
      if (!raw.ok) return null
      return await decodeImageBlob(await raw.blob(), name)
    }
    return null
  } catch {
    return null
  }
}
