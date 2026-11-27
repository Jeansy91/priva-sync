import { useEffect, useRef, useState } from 'react'
import { AlertCircle, Loader2 } from 'lucide-react'
import { useTranslation } from '@privasync/i18n'

const SAMPLE = 'The quick brown fox jumps over the lazy dog'
const PANGRAM_DE = 'Zwölf Boxkämpfer jagen Viktor quer über den großen Sylter Deich'
const NUMERALS = '0123456789 — !?"\'.,;:'
const SIZES = [12, 16, 20, 28, 40, 56, 80]

// FontViewer registriert eine FontFace direkt aus dem Blob und rendert
// Sample-Strings in mehreren Größen. Keine Drittlibrary nötig — FontFace
// frisst TTF/OTF/WOFF/WOFF2 nativ.
export function FontViewer({ blob, name }: { blob: Blob; name: string }) {
  const { t } = useTranslation()
  const containerRef = useRef<HTMLDivElement | null>(null)
  const [family, setFamily] = useState<string | null>(null)
  const [error,  setError]  = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    let fontFace: FontFace | null = null
    let blobUrl: string | null = null
    const fam = `priva-font-${Math.random().toString(36).slice(2)}`

    ;(async () => {
      try {
        const buf = await blob.arrayBuffer()
        if (cancelled) return
        // FontFace mit ArrayBuffer ist die robuste Variante; manche Browser
        // schauen aber auch auf das Blob-MIME -> Blob-URL als Fallback.
        try {
          fontFace = new FontFace(fam, buf)
          await fontFace.load()
        } catch (e1) {
          // eslint-disable-next-line no-console
          console.warn('FontFace from ArrayBuffer failed, retrying via url()', e1)
          blobUrl = URL.createObjectURL(blob)
          fontFace = new FontFace(fam, `url(${blobUrl})`)
          await fontFace.load()
        }
        if (cancelled) return
        ;(document as any).fonts?.add?.(fontFace)
        setFamily(fam)
      } catch (err) {
        // eslint-disable-next-line no-console
        console.error('FontViewer load failed', err)
        if (!cancelled) setError(t('files.fontLoadFailed'))
      }
    })()

    return () => {
      cancelled = true
      if (fontFace) {
        try { (document as any).fonts?.delete?.(fontFace) } catch {}
      }
      if (blobUrl) URL.revokeObjectURL(blobUrl)
    }
  }, [blob, t])

  if (error) {
    return (
      <div className="flex items-center justify-center gap-2 py-12 text-sm text-destructive">
        <AlertCircle className="h-4 w-4" />
        <span>{error}</span>
      </div>
    )
  }
  if (!family) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    )
  }

  return (
    <div
      ref={containerRef}
      className="p-6"
      style={{ ['--priva-font' as any]: family }}
    >
      <div className="border-b border-border pb-4 mb-6">
        <div
          className="text-3xl font-medium truncate"
          style={{ fontFamily: 'var(--priva-font), serif' }}
          title={name}
        >
          {name.replace(/\.[^.]+$/, '')}
        </div>
        <div className="text-xs text-muted-foreground mt-1">{name}</div>
      </div>

      <div className="space-y-6" style={{ fontFamily: 'var(--priva-font), serif' }}>
        {SIZES.map((s) => (
          <div key={s}>
            <div className="text-[10px] text-muted-foreground mb-1 font-sans">{s} px</div>
            <div style={{ fontSize: s, lineHeight: 1.2 }}>{SAMPLE}</div>
          </div>
        ))}
        <div>
          <div className="text-[10px] text-muted-foreground mb-1 font-sans">Umlaute</div>
          <div style={{ fontSize: 32, lineHeight: 1.2 }}>{PANGRAM_DE}</div>
        </div>
        <div>
          <div className="text-[10px] text-muted-foreground mb-1 font-sans">Zahlen + Symbole</div>
          <div style={{ fontSize: 32, lineHeight: 1.2 }}>{NUMERALS}</div>
        </div>
      </div>
    </div>
  )
}
