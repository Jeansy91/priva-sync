import { Button, Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { fileName, type PurgeTarget } from './trashHelpers'

interface PurgeDialogProps {
  target:  PurgeTarget | null
  onClose: () => void
  /** Resolves the right action based on `target.kind`. */
  onConfirm: (target: PurgeTarget) => void | Promise<void>
}

/**
 * Confirmation dialog for all four purge flavours (single entry, all
 * entries, current selection, an arbitrary list). The body text picks
 * the right phrasing based on the target's kind + count.
 */
export function PurgeDialog({ target, onClose, onConfirm }: PurgeDialogProps) {
  const { t } = useTranslation()

  const description =
    target?.kind === 'one'
      ? t('trash.dialogDescOne', { name: fileName(target.entry.path) })
      : target?.kind === 'all'
        ? (target.count === 1
            ? t('trash.dialogDescAllOne')
            : t('trash.dialogDescAllMany', { count: target.count }))
        : target?.kind === 'selected'
          ? (target.count === 1
              ? t('trash.dialogDescSelectedOne')
              : t('trash.dialogDescSelectedMany', { count: target.count }))
          : target?.kind === 'list'
            ? (target.entries.length === 1
                ? t('trash.dialogDescSelectedOne')
                : t('trash.dialogDescSelectedMany', { count: target.entries.length }))
            : null

  return (
    <Dialog open={target !== null} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('trash.dialogTitle')}</DialogTitle>
          <DialogDescription>
            {description}
            {' '}{t('trash.dialogIrreversible')}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button
            variant="destructive"
            onClick={() => {
              if (!target) return
              const t = target
              onClose()
              void onConfirm(t)
            }}
          >
            {t('trash.dialogConfirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
