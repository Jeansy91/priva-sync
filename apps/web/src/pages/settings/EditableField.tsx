import { Check, Pencil, X } from 'lucide-react'
import { Button, Input, Label } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'

// Profile field rendered as a pseudo-input plus pencil action. The edit mode
// turns the pseudo-input into a real <input> with check/cancel icon buttons.
// Pulled out of SettingsPage to keep that file from growing further; this is
// the only consumer today, but the pattern is self-contained.
export interface EditableFieldProps {
  id: string
  label: string
  value: string
  draft: string
  editing: boolean
  busy: boolean
  editLabel: string
  type?: string
  editPlaceholder?: string
  onDraftChange: (v: string) => void
  onStartEdit: () => void
  onSave: () => void
  onCancel: () => void
}

export function EditableField({
  id, label, value, draft, editing, busy, type, editPlaceholder, editLabel,
  onDraftChange, onStartEdit, onSave, onCancel,
}: EditableFieldProps) {
  const { t } = useTranslation()
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </Label>
      {editing ? (
        <div className="flex items-center gap-2">
          <Input
            id={id}
            type={type}
            value={draft}
            onChange={(e) => onDraftChange(e.target.value)}
            disabled={busy}
            placeholder={editPlaceholder}
            aria-label={editPlaceholder ?? label}
            autoFocus
            onKeyDown={(e) => {
              if (e.key === 'Enter') { e.preventDefault(); onSave() }
              if (e.key === 'Escape') { e.preventDefault(); onCancel() }
            }}
            className="max-w-sm"
          />
          <Button
            type="button"
            size="icon"
            variant="default"
            onClick={onSave}
            disabled={busy}
            aria-label={t('common.save')}
            title={t('common.save')}
          >
            <Check className="h-4 w-4" />
          </Button>
          <Button
            type="button"
            size="icon"
            variant="outline"
            onClick={onCancel}
            disabled={busy}
            aria-label={t('common.cancel')}
            title={t('common.cancel')}
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
      ) : (
        <div className="flex items-center gap-2">
          <div
            id={id}
            className="flex h-9 max-w-sm min-w-0 flex-1 items-center rounded-md border border-input bg-muted/40 px-3 text-sm text-foreground"
            title={value}
          >
            <span className="truncate">{value}</span>
          </div>
          <Button
            type="button"
            size="icon"
            variant="ghost"
            onClick={onStartEdit}
            aria-label={editLabel}
            title={editLabel}
          >
            <Pencil className="h-4 w-4" />
          </Button>
        </div>
      )}
    </div>
  )
}
