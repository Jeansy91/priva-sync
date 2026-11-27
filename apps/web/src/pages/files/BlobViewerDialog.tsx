import { useEffect, useState } from 'react'
import { AlertCircle, Loader2 } from 'lucide-react'
import { Dialog, DialogContent, DialogClose } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { downloadFrom, type FileSource } from './fileSource'

// BlobViewerDialog ist die Fullscreen-Hülle für Viewer, die das gesamte Blob
// brauchen (EPUB, Comic). Er übernimmt Download + Lifecycle; den Inhalt
// liefert die per `render`-Prop übergebene Komponente.
export function BlobViewerDialog({
  source, path, name, onClose, render,
}: {
  source:  FileSource
  path:    string
  name:    string
  onClose: () => void
  render:  (blob: Blob, name: string) => React.ReactNode
}) {
  const { t } = useTranslation()
  const [blob,  setBlob]  = useState<Blob | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setBlob(null)
    setError(null)
    ;(async () => {
      try {
        const res = await downloadFrom(source, path)
        if (!res.ok) throw new Error('http')
        const b = await res.blob()
        if (!cancelled) setBlob(b)
      } catch {
        if (!cancelled) setError(t('files.previewLoadFailed'))
      }
    })()
    return () => { cancelled = true }
  }, [source, path, t])

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent
        className="max-w-none! max-h-none! w-screen! h-dvh! mx-0! p-0! rounded-none! border-0! shadow-none! bg-black/75! backdrop-blur-xs flex items-center justify-center"
        aria-label={t('files.previewTitle', { name })}
      >
        <div className="absolute top-0 inset-x-0 z-20 flex items-center justify-between gap-2 px-3 sm:px-6 py-3 bg-linear-to-b from-black/70 to-transparent text-white">
          <div className="truncate text-sm font-medium" title={name}>{name}</div>
          <DialogClose onClose={onClose} className="static" />
        </div>
        {error ? (
          <div className="flex items-center gap-2 text-sm text-white/80">
            <AlertCircle className="h-4 w-4" />
            <span>{error}</span>
          </div>
        ) : !blob ? (
          <Loader2 className="h-8 w-8 animate-spin text-white/60" />
        ) : (
          render(blob, name)
        )}
      </DialogContent>
    </Dialog>
  )
}
