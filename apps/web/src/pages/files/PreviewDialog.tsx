import { useEffect, useState } from 'react'
import { AlertCircle, Loader2, Music2 } from 'lucide-react'
import { Dialog, DialogContent, DialogClose, FileIcon } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { previewKindFor, mimeForName, type PreviewKind } from './previewKind'
import { downloadFrom, type FileSource } from './fileSource'

type Props = {
  source:  FileSource
  path:    string
  name:    string
  onClose: () => void
}

export function PreviewDialog({ source, path, name, onClose }: Props) {
  const { t }   = useTranslation()
  const kind    = previewKindFor(name)
  const [url,    setUrl]    = useState<string | null>(null)
  const [error,  setError]  = useState<string | null>(null)

  useEffect(() => {
    if (!kind) {
      setError(t('files.previewUnsupported'))
      return
    }
    let revoke: string | null = null
    let cancelled = false

    ;(async () => {
      try {
        const res = await downloadFrom(source, path)
        if (!res.ok) throw new Error('http')
        const blob = await res.blob()
        if (cancelled) return
        // Server sniff'd ggf. falsches Content-Type (SVG -> text/xml).
        // Mit Extension-MIME überschreiben, sonst rendert <img> SVG nicht.
        const mime  = mimeForName(name)
        const typed = mime && blob.type !== mime ? new Blob([blob], { type: mime }) : blob
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
  }, [source, path, kind, t])

  // Audio läuft in kompaktem Layout (Cover-Bereich + Player-Footer);
  // alle anderen Typen nutzen ein flexibles Vollbild-Layout.
  const compact = kind === 'audio'

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent
        className={
          compact
            ? 'max-w-md w-[95vw] p-0 flex flex-col'
            : 'max-w-5xl w-[95vw] h-[90vh] p-0 flex flex-col'
        }
        aria-label={t('files.previewTitle', { name })}
      >
        <div className="flex items-center justify-between px-4 py-2 border-b border-border shrink-0">
          <div className="flex items-center gap-2 min-w-0">
            <FileIcon name={name} className="h-4 w-4 shrink-0" />
            <div className="truncate text-sm font-medium" title={name}>{name}</div>
          </div>
          <DialogClose onClose={onClose} className="static" />
        </div>

        {error ? (
          <div className="flex items-center justify-center gap-2 px-4 py-12 text-sm text-destructive">
            <AlertCircle className="h-4 w-4" />
            <span>{error}</span>
          </div>
        ) : !url ? (
          <div className={compact
            ? 'flex items-center justify-center py-12'
            : 'flex-1 min-h-0 flex items-center justify-center bg-muted/30'}>
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
          </div>
        ) : (
          <PreviewBody kind={kind!} url={url} name={name} />
        )}
      </DialogContent>
    </Dialog>
  )
}

function PreviewBody({ kind, url, name }: { kind: PreviewKind; url: string; name: string }) {
  switch (kind) {
    case 'image':
      return (
        <div className="flex-1 min-h-0 overflow-auto bg-muted/30 flex items-center justify-center p-4">
          <img src={url} alt={name} className="max-w-full max-h-full object-contain" />
        </div>
      )
    case 'pdf':
      return (
        <iframe
          src={url}
          title={name}
          className="flex-1 min-h-0 w-full border-0 bg-muted/30"
        />
      )
    case 'video':
      return (
        <div className="flex-1 min-h-0 bg-black flex items-center justify-center">
          <video
            src={url}
            aria-label={name}
            controls
            autoPlay
            className="max-w-full max-h-full"
          />
        </div>
      )
    case 'audio':
      return (
        <>
          <div className="flex flex-col items-center justify-center gap-3 px-6 py-10 bg-muted/30">
            <div className="h-24 w-24 rounded-full bg-primary/10 flex items-center justify-center">
              <Music2 className="h-12 w-12 text-primary" />
            </div>
            <div className="text-sm font-medium truncate max-w-full" title={name}>{name}</div>
          </div>
          <div className="border-t border-border bg-background px-4 py-3 shrink-0">
            <audio
              src={url}
              aria-label={name}
              controls
              autoPlay
              className="w-full"
            />
          </div>
        </>
      )
  }
}
