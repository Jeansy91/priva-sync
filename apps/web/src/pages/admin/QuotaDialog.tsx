import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import {
  Button, Input, Label,
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter,
} from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { Loader2, AlertCircle } from 'lucide-react'
import { apiClient } from '../../store/auth'
import type { AdminUser } from '@privasync/api-client'

function makeSchema(t: (k: string) => string) {
  return z.object({
    quota_gb: z.coerce.number().min(0, t('admin.quotaDialog.minQuota')),
  })
}

type QuotaSchema = ReturnType<typeof makeSchema>
type QuotaForm = z.output<QuotaSchema>

interface QuotaDialogProps {
  user:      AdminUser | null
  onClose:   () => void
  onUpdated: (id: string, bytes: number) => void
}

/**
 * Quota editor for a single user, denominated in gigabytes for input
 * comfort. Converts to bytes before sending; the form is pre-filled
 * from the current quota.
 */
export function QuotaDialog({ user, onClose, onUpdated }: QuotaDialogProps) {
  const { t } = useTranslation()
  const [apiError, setApiError] = useState<string | null>(null)
  // z.coerce accepts any input, so the form's field values and the parsed
  // submit data have different types.
  const form = useForm<z.input<QuotaSchema>, unknown, QuotaForm>({
    resolver: zodResolver(makeSchema(t)),
    values: user ? { quota_gb: user.quota_bytes / 1024 / 1024 / 1024 } : { quota_gb: 0 },
  })

  const onSubmit = async (data: QuotaForm) => {
    if (!user) return
    setApiError(null)
    try {
      const bytes = Math.round(data.quota_gb * 1024 * 1024 * 1024)
      await apiClient.admin.setQuota(user.id, bytes)
      onUpdated(user.id, bytes)
      onClose()
    } catch (err: unknown) {
      const msg = (err as { body?: { error?: string } } | null)?.body?.error
      setApiError(msg ?? t('admin.errorGeneric'))
    }
  }

  return (
    <Dialog open={user !== null} onOpenChange={(o) => { if (!o) { onClose(); form.reset(); setApiError(null) } }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('admin.quotaDialog.title')}</DialogTitle>
          <DialogDescription>
            {t('admin.quotaDialog.descriptionPrefix')}<strong>{user?.username}</strong>{t('admin.quotaDialog.descriptionSuffix')}
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
          <div className="space-y-1">
            <Label htmlFor="quota_gb">{t('admin.quotaDialog.label')}</Label>
            <Input
              id="quota_gb"
              type="number"
              step="0.1"
              min="0"
              {...form.register('quota_gb')}
            />
            {form.formState.errors.quota_gb && (
              <p className="text-xs text-destructive">{form.formState.errors.quota_gb.message}</p>
            )}
          </div>

          {apiError && (
            <div className="flex items-center gap-2 text-sm text-destructive border border-destructive/50 bg-destructive/10 rounded-md px-3 py-2">
              <AlertCircle className="h-4 w-4 shrink-0" />
              {apiError}
            </div>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose} disabled={form.formState.isSubmitting}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={form.formState.isSubmitting}>
              {form.formState.isSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('admin.quotaDialog.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
