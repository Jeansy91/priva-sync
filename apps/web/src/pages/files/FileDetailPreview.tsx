import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { FileIcon } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { fetchMountPreviewBlob, fetchPreviewBlob } from './previewBlob'

// Either a sync-root file ({ rootID, path }) or a Shared-Mount file
// ({ mountID, path }). `path` is the mount-relative sub-path — "/" for a
// file-mount, the file's own path inside a dir-mount. Exactly one shape applies.
type Props = {
  name:      string
  onClick?:  () => void
} & (
  | { rootID: string; path: string; mountID?: undefined }
  | { mountID: string; path: string; rootID?: undefined }
)

// FileDetailPreview rendert ein Vorschau-Bild für die Detailansicht oder fällt
// auf ein Dateityp-Icon zurück. Lädt das Bild lazy, sobald sich die Quelle
// ändert, und gibt die Object-URL beim Unmount frei.
export function FileDetailPreview({ name, onClick, ...source }: Props) {
  const { t } = useTranslation()
  const [url,     setUrl]     = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const mountID = source.mountID
  const rootID  = source.rootID
  const path    = source.path

  useEffect(() => {
    let cancelled = false
    let objUrl: string | null = null
    setUrl(null)
    setLoading(true)
    ;(async () => {
      const blob = mountID
        ? await fetchMountPreviewBlob(mountID, path, name)
        : await fetchPreviewBlob(rootID!, path, name)
      if (cancelled) return
      if (blob) {
        objUrl = URL.createObjectURL(blob)
        setUrl(objUrl)
      }
      setLoading(false)
    })()
    return () => {
      cancelled = true
      if (objUrl) URL.revokeObjectURL(objUrl)
    }
  }, [mountID, rootID, path, name])

  const baseCls =
    'flex aspect-square w-full items-center justify-center rounded-sm border border-border bg-muted/50 overflow-hidden'

  const inner = loading ? (
    <Loader2 className="h-6 w-6 animate-spin text-muted-foreground/70" aria-label={t('files.loading')} />
  ) : url ? (
    <img src={url} alt={name} className="h-full w-full object-contain" draggable={false} />
  ) : (
    <FileIcon name={name} className="h-12 w-12 text-muted-foreground/70" />
  )

  if (!onClick) {
    return <div data-testid="file-detail-preview" className={baseCls}>{inner}</div>
  }
  return (
    <button
      type="button"
      data-testid="file-detail-preview"
      aria-label={t('files.detail.openPreview')}
      onClick={onClick}
      className={baseCls + ' cursor-pointer hover:bg-muted'}
    >
      {inner}
    </button>
  )
}
