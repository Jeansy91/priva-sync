import { useTranslation } from '@privasync/i18n'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Button } from '@privasync/ui'
import { Loader2, Trash2 } from 'lucide-react'
import type { DirEntry } from './helpers'

interface DeleteEntryDialogProps {
  target:    DirEntry | null
  loading:   boolean
  onCancel:  () => void
  onConfirm: () => void
}

/**
 * Single-entry trash confirmation. Same shell for files and directories;
 * the title + hint switch on kind.
 */
export function DeleteEntryDialog(props: DeleteEntryDialogProps) {
  const { target, loading, onCancel, onConfirm } = props
  const { t } = useTranslation()

  return (
    <Dialog open={target !== null} onOpenChange={(o) => { if (!o) onCancel() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {target?.kind === 'dir' ? t('files.deleteFolderTitle') : t('files.deleteFileTitle')}
          </DialogTitle>
          {target && (
            <p className="text-sm text-muted-foreground mt-1">
              <strong>{target.name}</strong>
              {target.kind === 'dir' ? t('files.deleteHintFolder') : t('files.deleteHintFile')}
            </p>
          )}
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={onCancel} disabled={loading}>
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
