import { Upload, Check, X } from 'lucide-react'
import { Button } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'

export interface UploadItem {
  name:     string
  progress: number
  done:     boolean
  error?:   string
}

export function UploadQueue({
  items,
  onClose,
}: {
  items:   UploadItem[]
  onClose: () => void
}) {
  const { t } = useTranslation()
  if (items.length === 0) return null

  const doneCount = items.filter((i) => i.done || i.error).length

  return (
    <div
      className="fixed bottom-4 right-4 z-40 w-80 rounded-md border border-border bg-popover shadow-lg flex flex-col"
      role="status"
      aria-label={t('files.uploadQueueTitle')}
    >
      <div className="flex items-center justify-between border-b border-border px-3 py-2 shrink-0">
        <div className="flex items-center gap-2 text-sm font-medium">
          <Upload className="h-3.5 w-3.5" />
          <span>{t('files.uploadQueueTitle')}</span>
          <span className="text-xs text-muted-foreground">
            {doneCount}/{items.length}
          </span>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="h-6 w-6"
          onClick={onClose}
          title={t('files.uploadQueueClose')}
          aria-label={t('files.uploadQueueClose')}
        >
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>
      <ul className="max-h-72 overflow-auto py-1">
        {items.map((item) => (
          <li key={item.name} className="px-3 py-1.5 space-y-1">
            <div className="flex items-center justify-between gap-2 text-xs">
              <span className="truncate text-foreground">{item.name}</span>
              {item.error
                ? <span className="text-destructive shrink-0">{item.error}</span>
                : item.done
                ? <Check className="h-3.5 w-3.5 text-green-600 shrink-0" />
                : <span className="text-muted-foreground shrink-0 tabular-nums">{item.progress}%</span>}
            </div>
            {!item.done && !item.error && (
              <div className="h-1 rounded-full bg-secondary overflow-hidden">
                <div
                  className="h-full rounded-full bg-primary transition-all"
                  style={{ width: `${item.progress}%` }}
                />
              </div>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}
