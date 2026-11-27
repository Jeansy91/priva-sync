import { Plus, Loader2 } from 'lucide-react'
import {
  Button, Input,
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter,
} from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'

export function CreateRootDialog({
  open,
  name,
  loading,
  error,
  onNameChange,
  onConfirm,
  onClose,
}: {
  open:         boolean
  name:         string
  loading:      boolean
  error:        string | null
  onNameChange: (v: string) => void
  onConfirm:    () => void
  onClose:      () => void
}) {
  const { t } = useTranslation()
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('files.createRootTitle')}</DialogTitle>
          <DialogDescription>
            {t('files.createRootDesc')}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-1">
          <Input
            placeholder={t('files.createRootPlaceholder')}
            value={name}
            onChange={(e) => onNameChange(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') onConfirm() }}
            autoFocus
            disabled={loading}
          />
          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={loading}>
            {t('common.cancel')}
          </Button>
          <Button onClick={onConfirm} disabled={loading || !name.trim()}>
            {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}
            {t('files.createRootSubmit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
