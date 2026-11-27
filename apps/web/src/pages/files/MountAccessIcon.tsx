import { Eye, FilePen } from 'lucide-react'
import { useTranslation } from '@privasync/i18n'

// Recipient-side access indicator for a Shared Mount: a file-pen glyph when
// the mount is writable, an eye when it is read-only. Both render in the same
// muted tone so the glyph — not colour — carries the meaning. `withLabel`
// appends the human-readable text (detail panel); icon-only callers (table
// column) rely on the aria-label/tooltip. See ADR-0010 / ADR-0012.
export function MountAccessIcon({ canWrite, withLabel = false }: { canWrite: boolean; withLabel?: boolean }) {
  const { t } = useTranslation()
  const label = canWrite ? t('files.mountInfoWritable') : t('files.mountInfoReadOnly')
  const Icon = canWrite ? FilePen : Eye
  return (
    <span aria-label={label} title={label} className="inline-flex items-center gap-1.5">
      <Icon className="h-3.5 w-3.5 text-muted-foreground/70" aria-hidden />
      {withLabel && <span>{label}</span>}
    </span>
  )
}
