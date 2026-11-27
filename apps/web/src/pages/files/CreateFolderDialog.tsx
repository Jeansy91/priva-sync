import { useEffect, useState } from 'react'
import { FolderPlus, Loader2 } from 'lucide-react'
import {
  Button, Input,
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter,
} from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'

export function CreateFolderDialog({
  open,
  parentPath,
  rootName,
  existingNames,
  loading,
  serverError,
  onConfirm,
  onClose,
}: {
  open:          boolean
  parentPath:    string
  rootName?:     string
  existingNames: string[]
  loading:       boolean
  serverError:   string | null
  onConfirm:     (name: string) => void
  onClose:       () => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [localError, setLocalError] = useState<string | null>(null)

  useEffect(() => {
    if (!open) { setName(''); setLocalError(null) }
  }, [open])

  const validate = (raw: string): string | null => {
    const v = raw.trim()
    if (!v) return t('files.createFolderErrorEmpty')
    if (v === '..' || v === '.' || v.includes('/') || v.includes('\\')) {
      return t('files.createFolderErrorChars')
    }
    if (existingNames.includes(v)) return t('files.createFolderErrorDuplicate')
    return null
  }

  const handleConfirm = () => {
    const err = validate(name)
    if (err) { setLocalError(err); return }
    onConfirm(name.trim())
  }

  const isRoot = parentPath === '/' || parentPath === ''
  const desc = isRoot
    ? t('files.createFolderDescRoot', { root: rootName ?? '' })
    : t('files.createFolderDescSub', { root: rootName ?? '', path: parentPath })

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('files.createFolderTitle')}</DialogTitle>
          <DialogDescription>{desc}</DialogDescription>
        </DialogHeader>
        <div className="space-y-1">
          <Input
            placeholder={t('files.createFolderPlaceholder')}
            value={name}
            onChange={(e) => { setName(e.target.value); setLocalError(null) }}
            onKeyDown={(e) => { if (e.key === 'Enter') handleConfirm() }}
            autoFocus
            disabled={loading}
          />
          {(localError || serverError) && (
            <p className="text-sm text-destructive">{localError ?? serverError}</p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={loading}>
            {t('common.cancel')}
          </Button>
          <Button onClick={handleConfirm} disabled={loading || !name.trim()}>
            {loading
              ? <Loader2 className="h-4 w-4 animate-spin" />
              : <FolderPlus className="h-4 w-4" />}
            {t('files.createFolderSubmit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
