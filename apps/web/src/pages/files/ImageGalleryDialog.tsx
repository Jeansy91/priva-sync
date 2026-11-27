import { useCallback, useEffect, useRef, useState } from 'react'
import { AlertCircle, ChevronLeft, ChevronRight, Loader2, Play, X } from 'lucide-react'
import { Dialog, DialogContent } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { previewKindFor, mimeForName } from './previewKind'
import { decodeImageBlob, needsClientDecode } from './decodeImage'
import { GalleryVideoPlayer } from './GalleryVideoPlayer'
import { downloadFrom, thumbnailFrom, type FileSource } from './fileSource'
import { useMediaStreamSrc } from './useMediaStreamSrc'

export type GalleryImage = { path: string; name: string }

type Props = {
  source:     FileSource
  images:     GalleryImage[]
  startIndex: number
  onClose:    () => void
}

function ThumbnailStrip({
  source, images, activeIndex, onSelect,
}: {
  source:      FileSource
  images:      GalleryImage[]
  activeIndex: number
  onSelect:    (i: number) => void
}) {
  const { t } = useTranslation()
  const listRef = useRef<HTMLDivElement | null>(null)
  const activeRef = useRef<HTMLButtonElement | null>(null)

  useEffect(() => {
    const el = activeRef.current
    if (el && typeof el.scrollIntoView === 'function') {
      el.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'center' })
    }
  }, [activeIndex])

  return (
    <div
      ref={listRef}
      data-testid="gallery-strip"
      className="relative z-20 shrink-0 flex items-center gap-2 overflow-x-auto px-4 py-3"
    >
      {images.map((img, i) => {
        const active = i === activeIndex
        return (
          <button
            key={img.path}
            ref={active ? activeRef : undefined}
            type="button"
            onClick={() => onSelect(i)}
            aria-label={t('files.galleryThumbnail', { n: i + 1, name: img.name })}
            aria-current={active ? 'true' : undefined}
            title={img.name}
            className={
              'shrink-0 h-16 w-16 rounded-md overflow-hidden border-2 transition-all ' +
              (active
                ? 'border-white scale-105'
                : 'border-transparent opacity-60 hover:opacity-100')
            }
          >
            <Thumbnail source={source} image={img} />
          </button>
        )
      })}
    </div>
  )
}

function Thumbnail({ source, image }: { source: FileSource; image: GalleryImage }) {
  const wrapRef = useRef<HTMLSpanElement | null>(null)
  const [url, setUrl] = useState<string | null>(null)
  const [visible, setVisible] = useState(false)

  // Lazy-Loading: Blob erst holen, wenn das Element sichtbar ist (±200px Puffer).
  useEffect(() => {
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
      { root: el.closest('[data-testid="gallery-strip"]'), rootMargin: '200px' },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [])

  const isVideo = previewKindFor(image.name) === 'video'

  useEffect(() => {
    if (!visible) return
    let revoke: string | null = null
    let cancelled = false
    ;(async () => {
      try {
        let res = await thumbnailFrom(source, image.path, 128)
        if (!res.ok) {
          // Bei Video kein Download-Fallback — volle Video-Datei wäre teuer.
          // Stattdessen Play-Icon-Fallback.
          if (isVideo) return
          // Bild: SVG/WebP/HEIC/TIFF via Volldownload nachladen, ggf. decoden.
          res = await downloadFrom(source, image.path)
          if (!res.ok) return
        }
        let blob = await res.blob()
        if (cancelled) return
        if (needsClientDecode(image.name)) {
          try { blob = await decodeImageBlob(blob, image.name) } catch { return }
          if (cancelled) return
        }
        const mime  = mimeForName(image.name)
        const typed = mime && blob.type !== mime && !blob.type.startsWith('image/')
          ? new Blob([blob], { type: mime })
          : blob
        const objUrl = URL.createObjectURL(typed)
        revoke = objUrl
        setUrl(objUrl)
      } catch {
        // Thumbnail-Fehler bleibt leer — Hauptansicht zeigt Fehler an.
      }
    })()
    return () => {
      cancelled = true
      if (revoke) URL.revokeObjectURL(revoke)
    }
  }, [visible, isVideo, source, image.path, image.name])

  return (
    <span ref={wrapRef} className="relative block h-full w-full bg-white/10">
      {url ? (
        <>
          <img src={url} alt="" className="h-full w-full object-cover" draggable={false} />
          {isVideo && (
            <span className="absolute inset-0 flex items-center justify-center bg-black/30">
              <Play className="h-5 w-5 text-white drop-shadow-sm" />
            </span>
          )}
        </>
      ) : isVideo ? (
        <span className="flex h-full w-full items-center justify-center">
          <Play className="h-5 w-5 text-white/80" />
        </span>
      ) : null}
    </span>
  )
}

export function ImageGalleryDialog({ source, images, startIndex, onClose }: Props) {
  const { t } = useTranslation()
  const [index, setIndex] = useState(startIndex)
  const [url,   setUrl]   = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => { setIndex(startIndex) }, [startIndex])

  const current = images[index]
  const isVideo = current ? previewKindFor(current.name) === 'video' : false

  // Video streamt direkt vom Server statt vorher komplett zu laden
  // (ADR-0067) — GalleryVideoPlayer bekommt die Grant-URL als src, kein
  // Voll-Blob-Download mehr wie beim Bildpfad unten.
  const { src: videoSrc, error: videoError } = useMediaStreamSrc(
    isVideo ? source : null,
    isVideo && current ? current.path : null,
  )

  useEffect(() => {
    if (!current || isVideo) return
    let revoke: string | null = null
    let cancelled = false
    setError(null)
    setUrl(null)

    ;(async () => {
      try {
        const res = await downloadFrom(source, current.path)
        if (!res.ok) throw new Error('http')
        let blob = await res.blob()
        if (cancelled) return
        if (needsClientDecode(current.name)) {
          blob = await decodeImageBlob(blob, current.name)
          if (cancelled) return
        }
        const mime  = mimeForName(current.name)
        const typed = mime && blob.type !== mime && !blob.type.startsWith('image/')
          ? new Blob([blob], { type: mime })
          : blob
        const objUrl = URL.createObjectURL(typed)
        revoke = objUrl
        setUrl(objUrl)
      } catch {
        if (!cancelled) setError(t('files.previewLoadFailed'))
      }
    })()

    return () => {
      cancelled = true
      if (revoke) URL.revokeObjectURL(revoke)
    }
  }, [source, current?.path, current?.name, isVideo, t])

  const canPrev = index > 0
  const canNext = index < images.length - 1

  const prev = useCallback(() => { if (canPrev) setIndex((i) => i - 1) }, [canPrev])
  const next = useCallback(() => { if (canNext) setIndex((i) => i + 1) }, [canNext])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'ArrowLeft')  prev()
      if (e.key === 'ArrowRight') next()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [prev, next])

  if (!current) return null

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent
        className="max-w-none! max-h-none! w-screen! h-dvh! mx-0! p-0! rounded-none! border-0! shadow-none! bg-black/75! backdrop-blur-xs flex flex-col"
        aria-label={t('files.galleryTitle', { name: current.name })}
      >
        {/* Top overlay: Name + Counter + Close */}
        <div className="absolute top-0 inset-x-0 z-20 flex items-center justify-between gap-2 px-3 sm:px-6 py-3 bg-linear-to-b from-black/70 to-transparent text-white">
          <div className="truncate text-sm font-medium" title={current.name}>{current.name}</div>
          <div className="flex items-center gap-4 text-xs opacity-80">
            <span className="tabular-nums">{index + 1} / {images.length}</span>
            <button
              type="button"
              onClick={onClose}
              aria-label={t('files.galleryClose')}
              className="rounded-full p-1 hover:bg-white/10 transition-colors"
            >
              <X className="h-5 w-5" />
            </button>
          </div>
        </div>

        {/* Bildbereich */}
        <div className="relative flex-1 min-h-0 flex items-center justify-center">
          {/* Backdrop für Bildbereich (Klick schließt) */}
          <div
            aria-hidden="true"
            onClick={onClose}
            className="absolute inset-0 z-0"
          />
          {isVideo ? (
            videoError ? (
              <div className="relative z-10 flex items-center gap-2 text-sm text-white/80">
                <AlertCircle className="h-4 w-4" />
                <span>{t('files.previewLoadFailed')}</span>
              </div>
            ) : !videoSrc ? (
              <Loader2 className="relative z-10 h-8 w-8 animate-spin text-white/60" />
            ) : (
              <GalleryVideoPlayer src={videoSrc} name={current.name} resetKey={current.path} />
            )
          ) : error ? (
            <div className="relative z-10 flex items-center gap-2 text-sm text-white/80">
              <AlertCircle className="h-4 w-4" />
              <span>{error}</span>
            </div>
          ) : !url ? (
            <Loader2 className="relative z-10 h-8 w-8 animate-spin text-white/60" />
          ) : (
            <img
              src={url}
              alt={current.name}
              className="relative z-10 max-w-[95vw] max-h-[95%] object-contain select-none"
              draggable={false}
            />
          )}

          {/* Pfeile — nur über Bildbereich */}
          <button
            type="button"
            aria-label={t('files.galleryPrev')}
            onClick={prev}
            disabled={!canPrev}
            className="absolute left-4 top-1/2 -translate-y-1/2 z-20 rounded-full p-3 text-white/80 hover:text-white hover:bg-white/10 transition-colors disabled:opacity-20 disabled:pointer-events-none"
          >
            <ChevronLeft className="h-8 w-8" />
          </button>
          <button
            type="button"
            aria-label={t('files.galleryNext')}
            onClick={next}
            disabled={!canNext}
            className="absolute right-4 top-1/2 -translate-y-1/2 z-20 rounded-full p-3 text-white/80 hover:text-white hover:bg-white/10 transition-colors disabled:opacity-20 disabled:pointer-events-none"
          >
            <ChevronRight className="h-8 w-8" />
          </button>
        </div>

        {/* Rondell als eigene Footer-Zeile */}
        <ThumbnailStrip
          source={source}
          images={images}
          activeIndex={index}
          onSelect={setIndex}
        />
      </DialogContent>
    </Dialog>
  )
}
