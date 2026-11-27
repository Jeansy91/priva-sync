import { useTranslation } from '@privasync/i18n'
import type { SyncRoot } from '@privasync/api-client'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Button, Input } from '@privasync/ui'
import { Loader2, Trash2 } from 'lucide-react'

interface DeleteRootDialogProps {
  target:     SyncRoot | null
  confirm:    string
  loading:    boolean
  setConfirm: (s: string) => void
  onCancel:   () => void
  onConfirm:  () => void
}

/**
 * Destructive confirmation for "delete entire sync root". Requires typing
 * the root's name in plain text before the submit button enables — same
 * pattern GitHub uses for repo deletion.
 */
export function DeleteRootDialog(props: DeleteRootDialogProps) {
  const { target, confirm, loading, setConfirm, onCancel, onConfirm } = props
  const { t } = useTranslation()

  return (
    <Dialog
      open={target !== null}
      onOpenChange={(o) => { if (!o) onCancel() }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('files.deleteRootTitle')}</DialogTitle>
          {target && (
            <DialogDescription className="mt-2 space-y-2">
              <span className="block">
                <strong>{target.name}</strong>{t('files.deleteRootWarningSuffix')}
              </span>
              <span className="block">
                {t('files.deleteRootConfirmHint')}
              </span>
            </DialogDescription>
          )}
        </DialogHeader>
        <Input
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          placeholder={target?.name}
          disabled={loading}
          autoFocus
        />
        <DialogFooter>
          <Button
            variant="outline"
            onClick={onCancel}
            disabled={loading}
          >
            {t('common.cancel')}
          </Button>
          <Button
            variant="destructive"
            onClick={onConfirm}
            disabled={loading || confirm !== target?.name}
          >
            {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4" />}
            {t('files.deleteRootSubmit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
