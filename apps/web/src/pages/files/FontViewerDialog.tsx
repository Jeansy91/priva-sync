import { useEffect, useState } from 'react'
import { AlertCircle, Loader2 } from 'lucide-react'
import { Dialog, DialogContent, DialogClose } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { FontViewer } from './FontViewer'
import { downloadFrom, type FileSource } from './fileSource'

export function FontViewerDialog({
  source, path, name, onClose,
}: {
  source:  FileSource
  path:    string
  name:    string
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [blob,  setBlob]  = useState<Blob | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
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
      <DialogContent className="max-w-3xl w-[95vw] h-[80vh] p-0 flex flex-col">
        <div className="flex items-center justify-between px-4 py-2 border-b border-border shrink-0">
          <div className="truncate text-sm font-medium" title={name}>{name}</div>
          <DialogClose onClose={onClose} className="static" />
        </div>
        <div className="flex-1 min-h-0 overflow-auto">
          {error ? (
            <div className="flex items-center justify-center gap-2 py-12 text-sm text-destructive">
              <AlertCircle className="h-4 w-4" />
              <span>{error}</span>
            </div>
          ) : !blob ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            </div>
          ) : (
            <FontViewer blob={blob} name={name} />
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
