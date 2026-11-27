import { useEffect, useRef, useState } from 'react'
import { Copy, Loader2 } from 'lucide-react'
import {
  Button, Input,
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter,
} from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'

export function CopyDialog({
  open,
  originalName,
  defaultName,
  existingNames,
  loading,
  serverError,
  onConfirm,
  onClose,
}: {
  open:          boolean
  originalName:  string
  defaultName:   string
  existingNames: string[]
  loading:       boolean
  serverError:   string | null
  onConfirm:     (name: string) => void
  onClose:       () => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState(defaultName)
  const [localError, setLocalError] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  // Pre-fill with the suggested copy name and select the basename so the user
  // can immediately retype while keeping the extension intact.
  useEffect(() => {
    if (!open) { setLocalError(null); return }
    setName(defaultName)
    setLocalError(null)
    const el = inputRef.current
    if (!el) return
    const dot = defaultName.lastIndexOf('.')
    const end = dot > 0 ? dot : defaultName.length
    requestAnimationFrame(() => { el.focus(); el.setSelectionRange(0, end) })
  }, [open, defaultName])

  const validate = (raw: string): string | null => {
    const v = raw.trim()
    if (!v) return t('files.copyErrorEmpty')
    if (v === '..' || v === '.' || v.includes('/') || v.includes('\\')) {
      return t('files.copyErrorChars')
    }
    if (v === originalName) return t('files.copyErrorUnchanged')
    if (existingNames.includes(v)) return t('files.copyErrorDuplicate')
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
          <DialogTitle>{t('files.copyTitle')}</DialogTitle>
          <DialogDescription>{t('files.copyDesc', { name: originalName })}</DialogDescription>
        </DialogHeader>
        <div className="space-y-1">
          <Input
            ref={inputRef}
            placeholder={t('files.copyPlaceholder')}
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
              : <Copy className="h-4 w-4" />}
            {t('files.copySubmit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
