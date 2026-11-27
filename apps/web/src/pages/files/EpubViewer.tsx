import { useEffect, useRef, useState } from 'react'
import { AlertCircle, ChevronLeft, ChevronRight, Loader2 } from 'lucide-react'
import { useTranslation } from '@privasync/i18n'

// EpubViewer rendert epub.js in einem Div, mit Prev/Next.
export function EpubViewer({ blob }: { blob: Blob; name: string }) {
  const { t } = useTranslation()
  const mountRef = useRef<HTMLDivElement | null>(null)
  const renditionRef = useRef<any>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    let bookInstance: any = null

    ;(async () => {
      try {
        const mod: any = await import('epubjs')
        const ePub = mod.default || mod
        const buf = await blob.arrayBuffer()
        if (cancelled) return
        bookInstance = ePub(buf)
        if (!mountRef.current) return
        const rendition = bookInstance.renderTo(mountRef.current, {
          width:  '100%',
          height: '100%',
          spread: 'auto',
        })
        renditionRef.current = rendition
        await rendition.display()
        setLoading(false)
      } catch {
        if (!cancelled) {
          setError(t('files.epubLoadFailed'))
          setLoading(false)
        }
      }
    })()

    return () => {
      cancelled = true
      try { renditionRef.current?.destroy?.() } catch {}
      try { bookInstance?.destroy?.() } catch {}
    }
  }, [blob, t])

  const prev = () => renditionRef.current?.prev?.()
  const next = () => renditionRef.current?.next?.()

  return (
    <div className="relative z-10 w-[90vw] h-[90vh] bg-background rounded-md overflow-hidden flex flex-col">
      <div className="flex-1 min-h-0 relative">
        <div ref={mountRef} className="absolute inset-0" />
        {loading && !error && (
          <div className="absolute inset-0 flex items-center justify-center gap-2 text-muted-foreground">
            <Loader2 className="h-6 w-6 animate-spin" />
            <span className="text-sm">{t('files.epubLoading')}</span>
          </div>
        )}
        {error && (
          <div className="absolute inset-0 flex items-center justify-center gap-2 text-destructive">
            <AlertCircle className="h-4 w-4" />
            <span className="text-sm">{error}</span>
          </div>
        )}
      </div>
      <div className="flex items-center justify-center gap-2 border-t border-border px-4 py-2 shrink-0">
        <button
          type="button"
          aria-label={t('files.epubPrev')}
          onClick={prev}
          className="rounded-md p-1.5 hover:bg-accent"
        >
          <ChevronLeft className="h-5 w-5" />
        </button>
        <button
          type="button"
          aria-label={t('files.epubNext')}
          onClick={next}
          className="rounded-md p-1.5 hover:bg-accent"
        >
          <ChevronRight className="h-5 w-5" />
        </button>
      </div>
    </div>
  )
}
