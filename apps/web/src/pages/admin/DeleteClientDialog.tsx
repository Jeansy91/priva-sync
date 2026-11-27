import { useState } from 'react'
import { useTranslation } from '@privasync/i18n'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Button } from '@privasync/ui'
import { AlertCircle, Loader2, Trash2 } from 'lucide-react'
import { apiClient } from '../../store/auth'
import type { AdminClient } from '@privasync/api-client'

interface DeleteClientDialogProps {
  target:    AdminClient | null
  onCancel:  () => void
  onDeleted: () => void | Promise<void>
}

export function DeleteClientDialog({ target, onCancel, onDeleted }: DeleteClientDialogProps) {
  const { t } = useTranslation()
  const [deleting, setDeleting] = useState(false)
  const [apiError, setApiError] = useState<string | null>(null)

  const handleCancel = () => { if (!deleting) { onCancel(); setApiError(null) } }

  const handleConfirm = async () => {
    if (!target) return
    setDeleting(true)
    setApiError(null)
    try {
      await apiClient.admin.deleteClient(target.id)
      await onDeleted()
      onCancel()
    } catch (err: unknown) {
      const msg = (err as { body?: { error?: string } } | null)?.body?.error
      setApiError(msg ?? t('admin.clients.deleteDialog.errorGeneric'))
    } finally {
      setDeleting(false)
    }
  }

  return (
    <Dialog open={target !== null} onOpenChange={(o) => { if (!o) handleCancel() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('admin.clients.deleteDialog.title', { name: target?.name })}</DialogTitle>
          <p className="text-sm text-muted-foreground mt-1">
            {t('admin.clients.deleteDialog.hint')}
          </p>
        </DialogHeader>

        {apiError && (
          <div className="flex items-center gap-2 text-sm text-destructive border border-destructive/50 bg-destructive/10 rounded-md px-3 py-2">
            <AlertCircle className="h-4 w-4 shrink-0" />
            {apiError}
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={handleCancel} disabled={deleting}>
            {t('common.cancel')}
          </Button>
          <Button variant="destructive" onClick={handleConfirm} disabled={deleting}>
            {deleting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4" />}
            {t('common.delete')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
