import { Loader2, Trash2 } from 'lucide-react'
import {
  Button,
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter,
} from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'

type Props = {
  open:    boolean
  count:   number
  loading: boolean
  onConfirm: () => void
  onClose:   () => void
}

export function BulkDeleteDialog({ open, count, loading, onConfirm, onClose }: Props) {
  const { t } = useTranslation()
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('files.bulkDeleteTitle', { count })}</DialogTitle>
          <p className="text-sm text-muted-foreground mt-1">{t('files.bulkDeleteHint')}</p>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={loading}>
            {t('common.cancel')}
          </Button>
          <Button variant="destructive" onClick={onConfirm} disabled={loading}>
            {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4" />}
            {t('files.deleteToTrash')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
