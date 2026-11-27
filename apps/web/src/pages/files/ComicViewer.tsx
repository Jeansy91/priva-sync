import { useEffect, useMemo, useRef, useState } from 'react'
import {
  AlertCircle, BookOpen, ChevronLeft, ChevronRight, Columns2,
  Languages, Loader2, Square, SquareSplitHorizontal,
} from 'lucide-react'
import { useTranslation } from '@privasync/i18n'

const IMG_EXT = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'avif', 'bmp'])

type PageEntry = { name: string; getBlob: () => Promise<Blob> }

// ComicViewer entpackt CBZ (ZIP) und CBR (RAR), sortiert die Bilddateien
// natürlich (page2 vor page10) und blättert seitenweise. Unterstützt zudem
// Doppelseiten-Modus mit RTL (Manga) und separat-stehender Titelseite.
export function ComicViewer({ blob, name }: { blob: Blob; name: string }) {
  const { t } = useTranslation()
  const [pages, setPages] = useState<PageEntry[] | null>(null)
  const [index, setIndex] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const [double, setDouble] = useState(false)
  const [rtl,    setRTL]    = useState(false)
  const [cover,  setCover]  = useState(true)
  const [urls,   setUrls]   = useState<(string | null)[]>([null, null])
  const revokeRef = useRef<string[]>([])

  const isRAR = /\.cbr$/i.test(name)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const entries = isRAR
          ? await extractRAR(blob)
          : await extractZIP(blob)
        if (cancelled) return
        if (entries.length === 0) throw new Error('no images')
        setPages(entries)
        setIndex(0)
      } catch (err) {
        // eslint-disable-next-line no-console
        console.error('ComicViewer extract failed', err)
        if (!cancelled) setError(t('files.comicLoadFailed'))
      }
    })()
    return () => { cancelled = true }
  }, [blob, isRAR, t])

  // Welche Indexe gehören zum aktuellen Spread?
  // - single: [index]
  // - double + cover-separate + index===0: [0]
  // - double sonst:                        [index, index+1]
  const spread = useMemo<number[]>(() => {
    if (!pages) return []
    if (!double) return [index]
    if (cover && index === 0) return [0]
    const next = index + 1
    return next < pages.length ? [index, next] : [index]
  }, [pages, index, double, cover])

  // Spread-Bilder laden — sequenziell, alte URLs revoken.
  useEffect(() => {
    if (!pages || spread.length === 0) return
    let cancelled = false
    setUrls(new Array(spread.length).fill(null))
    ;(async () => {
      const next: string[] = []
      for (let i = 0; i < spread.length; i++) {
        const idx = spread[i]
        try {
          const b = await pages[idx].getBlob()
          if (cancelled) return
          next.push(URL.createObjectURL(b))
        } catch {
          if (!cancelled) setError(t('files.comicLoadFailed'))
          return
        }
      }
      if (cancelled) {
        next.forEach((u) => URL.revokeObjectURL(u))
        return
      }
      revokeRef.current.forEach((u) => URL.revokeObjectURL(u))
      revokeRef.current = next
      setUrls(next)
    })()
    return () => { cancelled = true }
  }, [pages, spread, t])

  useEffect(() => () => {
    revokeRef.current.forEach((u) => URL.revokeObjectURL(u))
  }, [])


  const totalPages = pages?.length ?? 0
  const step       = double ? 2 : 1


  const prev = () => {
    setIndex((i) => {
      if (i === 0) return 0
      // Wenn die Titelseite separat steht und wir aktuell bei 1 sind: zurück zu 0.
      if (double && cover && i === 1) return 0
      return Math.max(0, i - step)
    })
  }
  const next = () => {
    setIndex((i) => {
      // Aus Titelseiten-Solo eine echte Doppelseite (1+2).
      if (double && cover && i === 0 && totalPages > 1) return 1
      return Math.min(totalPages - 1, i + step)
    })
  }

  // Toggle-Handler:
  // - Doppelseiten: kein Index-Shift nötig.
  // - Titelseite-separat: Spread verschiebt sich um genau eine Seite,
  //   außer bei i=0 (Solo bzw. Paar ab Seite 1). Symmetrisch ±1 -> Toggle
  //   ist idempotent ohne Drift.
  const toggleDouble = () => setDouble((v) => !v)
  const toggleCover = () => {
    const nextCover = !cover
    setCover(nextCover)
    if (double && index !== 0) {
      setIndex(nextCover ? index - 1 : index + 1)
    }
  }

  if (error) {
    return (
      <div className="relative z-10 w-[80vw] h-[80vh] flex items-center justify-center gap-2 text-destructive bg-background rounded-md">
        <AlertCircle className="h-4 w-4" />
        <span className="text-sm">{error}</span>
      </div>
    )
  }
  if (!pages) {
    return (
      <div className="relative z-10 w-[80vw] h-[80vh] flex items-center justify-center gap-2 text-muted-foreground bg-background rounded-md">
        <Loader2 className="h-6 w-6 animate-spin" />
        <span className="text-sm">{t('files.comicLoading')}</span>
      </div>
    )
  }

  // Bei RTL die Spread-Reihenfolge umkehren, Indexe bleiben unverändert.
  const displayUrls = rtl ? [...urls].reverse() : urls
  const counterText = spread.length === 2
    ? `${spread[0] + 1}-${spread[1] + 1} / ${totalPages}`
    : `${spread[0] + 1} / ${totalPages}`

  return (
    <div className="relative z-10 w-[90vw] h-[90vh] bg-background rounded-md overflow-hidden flex flex-col">
      <div className="flex-1 min-h-0 relative bg-muted/30 flex items-center justify-center p-2">
        {displayUrls.map((u, i) => {
          const isPair  = displayUrls.length === 2
          const justify = isPair
            ? (i === 0 ? 'justify-end' : 'justify-start')
            : 'justify-center'
          // Buchspine-Schatten: linkes Bild wirft nach rechts, rechtes Bild
          // spiegelt nach links — gleicher Schatten an beiden Innenkanten.
          const shadow = isPair
            ? (i === 0
              ? 'shadow-[6px_0_18px_-4px_rgba(0,0,0,0.55)]'
              : 'shadow-[-6px_0_18px_-4px_rgba(0,0,0,0.55)]')
            : ''
          return (
            <div
              key={i}
              className={'flex-1 min-w-0 min-h-0 h-full flex items-center ' + justify}
            >
              {u ? (
                <img
                  src={u}
                  alt=""
                  className={'max-w-full max-h-full object-contain select-none ' + shadow}
                  draggable={false}
                />
              ) : (
                <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
              )}
            </div>
          )
        })}
      </div>
      <div className="flex items-center justify-between gap-3 border-t border-border px-4 py-2 shrink-0">
        {/* Modus-Schalter links */}
        <div className="flex items-center gap-1">
          <button
            type="button"
            aria-label={t('files.comicTogglePages')}
            aria-pressed={double}
            onClick={toggleDouble}
            className={
              'rounded-md p-1.5 hover:bg-accent ' +
              (double ? 'text-foreground bg-accent' : 'text-muted-foreground')
            }
            title={t('files.comicTogglePages')}
          >
            {double ? <Columns2 className="h-4 w-4" /> : <Square className="h-4 w-4" />}
          </button>
          <button
            type="button"
            aria-label={t('files.comicToggleRTL')}
            aria-pressed={rtl}
            onClick={() => setRTL((v) => !v)}
            disabled={!double}
            className={
              'rounded-md p-1.5 hover:bg-accent disabled:opacity-30 disabled:pointer-events-none ' +
              (rtl ? 'text-foreground bg-accent' : 'text-muted-foreground')
            }
            title={t('files.comicToggleRTL')}
          >
            <Languages className="h-4 w-4" />
          </button>
          <button
            type="button"
            aria-label={t('files.comicToggleCover')}
            aria-pressed={cover}
            onClick={toggleCover}
            disabled={!double}
            className={
              'rounded-md p-1.5 hover:bg-accent disabled:opacity-30 disabled:pointer-events-none ' +
              (cover ? 'text-foreground bg-accent' : 'text-muted-foreground')
            }
            title={t('files.comicToggleCover')}
          >
            <SquareSplitHorizontal className="h-4 w-4" />
          </button>
        </div>

        {/* Navigation mittig */}
        <div className="flex items-center gap-3">
          <button
            type="button"
            aria-label={t('files.epubPrev')}
            onClick={prev}
            disabled={index === 0}
            className="rounded-md p-1.5 hover:bg-accent disabled:opacity-30 disabled:pointer-events-none"
          >
            <ChevronLeft className="h-5 w-5" />
          </button>
          <span className="text-xs text-muted-foreground tabular-nums">{counterText}</span>
          <button
            type="button"
            aria-label={t('files.epubNext')}
            onClick={next}
            disabled={spread[spread.length - 1] >= totalPages - 1}
            className="rounded-md p-1.5 hover:bg-accent disabled:opacity-30 disabled:pointer-events-none"
          >
            <ChevronRight className="h-5 w-5" />
          </button>
        </div>

        {/* Filler rechts, damit die Navigation mittig bleibt */}
        <div className="w-24 flex items-center justify-end opacity-50">
          <BookOpen className="h-4 w-4" />
        </div>
      </div>
    </div>
  )
}

async function extractZIP(blob: Blob): Promise<PageEntry[]> {
  const mod: any = await import('jszip')
  const JSZip = mod.default || mod
  const zip = await JSZip.loadAsync(await blob.arrayBuffer())
  const entries: PageEntry[] = []
  zip.forEach((relPath: string, file: any) => {
    if (file.dir) return
    if (!isImageName(relPath)) return
    entries.push({
      name: relPath,
      getBlob: async () => {
        const buf: ArrayBuffer = await file.async('arraybuffer')
        return new Blob([buf], { type: imageMimeForExt(relPath) })
      },
    })
  })
  entries.sort((a, b) => naturalCompare(a.name, b.name))
  return entries
}

async function extractRAR(blob: Blob): Promise<PageEntry[]> {
  const mod: any = await import('node-unrar-js')
  const buf = new Uint8Array(await blob.arrayBuffer())
  const extractor = await mod.createExtractorFromData({ data: buf })

  const names: string[] = []
  for (const h of extractor.getFileList().fileHeaders as Iterable<any>) {
    if (!h.flags?.directory && isImageName(h.name)) names.push(h.name)
  }
  names.sort(naturalCompare)

  return names.map((n) => ({
    name: n,
    getBlob: async () => {
      const result = extractor.extract({ files: [n] })
      const it = result.files as Iterable<any>
      for (const f of it) {
        if (f.extraction) {
          const u8 = f.extraction as Uint8Array
          const copy = new Uint8Array(u8.byteLength)
          copy.set(u8)
          return new Blob([copy], { type: imageMimeForExt(n) })
        }
      }
      throw new Error('rar: no extraction for ' + n)
    },
  }))
}

function isImageName(p: string): boolean {
  const ext = p.split('.').pop()?.toLowerCase()
  return !!ext && IMG_EXT.has(ext)
}

function imageMimeForExt(n: string): string {
  const ext = n.split('.').pop()?.toLowerCase()
  switch (ext) {
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

function naturalCompare(a: string, b: string): number {
  const re = /(\d+)|(\D+)/g
  const at = a.match(re) ?? []
  const bt = b.match(re) ?? []
  const n  = Math.min(at.length, bt.length)
  for (let i = 0; i < n; i++) {
    const aa = at[i], bb = bt[i]
    const an = /^\d+$/.test(aa), bn = /^\d+$/.test(bb)
    if (an && bn) {
      const d = parseInt(aa, 10) - parseInt(bb, 10)
      if (d !== 0) return d
    } else {
      const c = aa.localeCompare(bb)
      if (c !== 0) return c
    }
  }
  return at.length - bt.length
}
