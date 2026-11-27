import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import {
  Button, Input, Label, Select,
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter,
} from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { Loader2, UserPlus, AlertCircle } from 'lucide-react'
import { apiClient } from '../../store/auth'
import type { AdminUser } from '@privasync/api-client'

function makeSchema(t: (k: string) => string) {
  return z.object({
    username: z.string().min(3, t('admin.createDialog.minUsername')),
    email: z.string().email(t('admin.createDialog.invalidEmail')),
    role: z.enum(['user', 'admin']),
  })
}

type CreateUserForm = { username: string; email: string; role: 'user' | 'admin' }

interface CreateUserDialogProps {
  open: boolean
  onClose: () => void
  onCreated: (user: AdminUser) => void
}

/**
 * Admin-only "create user" form. Posts to /admin/users and hands the
 * fresh row back to the page so it can be prepended to the list.
 */
export function CreateUserDialog({ open, onClose, onCreated }: CreateUserDialogProps) {
  const { t } = useTranslation()
  const [apiError, setApiError] = useState<string | null>(null)
  const form = useForm<CreateUserForm>({
    resolver: zodResolver(makeSchema(t)),
    defaultValues: { role: 'user' },
  })

  const onSubmit = async (data: CreateUserForm) => {
    setApiError(null)
    try {
      const user = await apiClient.admin.createUser(data)
      onCreated(user)
      form.reset()
      onClose()
    } catch (err: unknown) {
      const msg = (err as { body?: { error?: string } } | null)?.body?.error
      setApiError(msg ?? t('admin.createDialog.errorGeneric'))
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) { onClose(); form.reset(); setApiError(null) } }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('admin.createDialog.title')}</DialogTitle>
          <DialogDescription>
            {t('admin.createDialog.description')}
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
          <div className="space-y-1">
            <Label htmlFor="cu-username">{t('admin.createDialog.username')}</Label>
            <Input id="cu-username" {...form.register('username')} />
            {form.formState.errors.username && (
              <p className="text-xs text-destructive">{form.formState.errors.username.message}</p>
            )}
          </div>
          <div className="space-y-1">
            <Label htmlFor="cu-email">{t('admin.createDialog.email')}</Label>
            <Input id="cu-email" type="email" {...form.register('email')} />
            {form.formState.errors.email && (
              <p className="text-xs text-destructive">{form.formState.errors.email.message}</p>
            )}
          </div>
          <div className="space-y-1">
            <Label>{t('admin.createDialog.role')}</Label>
            <Select
              value={form.watch('role')}
              onChange={(v) => form.setValue('role', v as 'user' | 'admin')}
              options={[
                { value: 'user',  label: t('admin.roleUser') },
                { value: 'admin', label: t('admin.roleAdmin') },
              ]}
            />
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
              {form.formState.isSubmitting
                ? <Loader2 className="h-4 w-4 animate-spin" />
                : <UserPlus className="h-4 w-4" />}
              {t('admin.createDialog.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
