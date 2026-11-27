import { useEffect, useRef, useState } from 'react'
import { Loader2, Pencil } from 'lucide-react'
import {
  Button, Input,
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter,
} from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'

export function RenameDialog({
  open,
  currentName,
  existingNames,
  loading,
  serverError,
  onConfirm,
  onClose,
}: {
  open:          boolean
  currentName:   string
  existingNames: string[]
  loading:       boolean
  serverError:   string | null
  onConfirm:     (name: string) => void
  onClose:       () => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState(currentName)
  const [localError, setLocalError] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  // Reset to the current name on each open and pre-select the basename (the
  // part before the extension) so a quick rename keeps the suffix intact.
  useEffect(() => {
    if (!open) { setLocalError(null); return }
    setName(currentName)
    setLocalError(null)
    const el = inputRef.current
    if (!el) return
    const dot = currentName.lastIndexOf('.')
    const end = dot > 0 ? dot : currentName.length
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(0, end) })
  }, [open, currentName])

  const validate = (raw: string): string | null => {
    const v = raw.trim()
    if (!v) return t('files.renameErrorEmpty')
    if (v === '..' || v === '.' || v.includes('/') || v.includes('\\')) {
      return t('files.renameErrorChars')
    }
    if (v === currentName) return t('files.renameErrorUnchanged')
    if (existingNames.includes(v)) return t('files.renameErrorDuplicate')
    return null
  }

  const handleConfirm = () => {
    const err = validate(name)
    if (err) { setLocalError(err); return }
    onConfirm(name.trim())
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('files.renameTitle')}</DialogTitle>
          <DialogDescription>{t('files.renameDesc', { name: currentName })}</DialogDescription>
        </DialogHeader>
        <div className="space-y-1">
          <Input
            ref={inputRef}
            placeholder={t('files.renamePlaceholder')}
            value={name}
            onChange={(e) => { setName(e.target.value); setLocalError(null) }}
            onKeyDown={(e) => { if (e.key === 'Enter') handleConfirm() }}
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
              : <Pencil className="h-4 w-4" />}
            {t('files.renameSubmit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
