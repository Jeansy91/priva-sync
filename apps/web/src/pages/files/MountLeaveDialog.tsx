import { useTranslation } from '@privasync/i18n'
import type { SharedMount } from '@privasync/api-client'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Button } from '@privasync/ui'
import { Loader2, Trash2, AlertCircle } from 'lucide-react'

interface MountLeaveDialogProps {
  target:   SharedMount | null
  error:    string | null
  loading:  boolean
  onCancel: () => void
  onConfirm: () => void
}

/**
 * "Leave shared mount" confirmation. Mirrors the destructive style of
 * DeleteRootDialog because dropping a mount removes the recipient's
 * access to the giver's data.
 */
export function MountLeaveDialog(props: MountLeaveDialogProps) {
  const { target, error, loading, onCancel, onConfirm } = props
  const { t } = useTranslation()

  return (
    <Dialog
      open={target !== null}
      onOpenChange={(o) => { if (!o) onCancel() }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('files.mountLeaveTitle')}</DialogTitle>
          {target && (
            <DialogDescription>
              {t('files.mountLeaveDescription', { name: target.name })}
            </DialogDescription>
          )}
        </DialogHeader>
        {error && (
          <div className="mx-1 flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            <AlertCircle className="h-4 w-4 shrink-0" />
            {error}
          </div>
        )}
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
            disabled={loading}
          >
            {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4" />}
            {t('files.mountLeaveConfirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
