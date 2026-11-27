import { useState } from 'react'
import {
  Users, UserPlus, Loader2, CheckCircle, AlertCircle,
} from 'lucide-react'
import { Button } from '@privasync/ui'
import { useAuthStore } from '../store/auth'
import type { AdminUser } from '@privasync/api-client'
import { useTranslation } from '@privasync/i18n'
import { useScrolled } from '../hooks/useScrolled'
import { useTableSort } from '../hooks/useTableSort'
import { SortableTh } from '../components/SortableTh'
import { CreateUserDialog } from './admin/CreateUserDialog'
import { QuotaDialog } from './admin/QuotaDialog'
import { UserRow } from './admin/UserRow'
import { useAdminUsers } from './admin/useAdminUsers'
import { PageHeader } from '../components/PageHeader'

const SUCCESS_TTL_MS = 3000

export default function AdminPage() {
  const { t } = useTranslation()
  const { scrolled, onScroll } = useScrolled()
  const selfID = useAuthStore((s) => s.user?.id)

  const {
    users, setUsers, loading, error, success, setSuccess,
    togglingActive, togglingRole, unlocking, recomputing,
    handleUnlock, handleRecompute, handleToggleRole, handleToggleActive,
  } = useAdminUsers(t)

  const usersSort = useTableSort(users, {
    storageKey: 'admin-users',
    comparators: {
      user:    (a, b) => a.username.localeCompare(b.username),
      role:    (a, b) => a.role.localeCompare(b.role),
      storage: (a, b) => a.used_bytes - b.used_bytes,
      twofa:   (a, b) => Number(a.totp_enabled) - Number(b.totp_enabled),
      status:  (a, b) => {
        const sa = a.is_locked ? 2 : a.is_active ? 0 : 1
        const sb = b.is_locked ? 2 : b.is_active ? 0 : 1
        return sa - sb
      },
    },
  })

  const [createOpen, setCreateOpen] = useState(false)
  const [quotaTarget, setQuotaTarget] = useState<AdminUser | null>(null)

  const flashSuccess = (msg: string) => {
    setSuccess(msg)
    setTimeout(() => setSuccess(null), SUCCESS_TTL_MS)
  }

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title={t('admin.title')}
        subtitle={users.length === 1 ? t('admin.subtitleOne') : t('admin.subtitleMany', { count: users.length })}
        scrolled={scrolled}
        actions={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <UserPlus className="h-3.5 w-3.5" />
            {t('admin.create')}
          </Button>
        }
      />

      <div className="flex-1 overflow-auto" onScroll={onScroll}>
        <div className="max-w-7xl mx-auto p-4 sm:p-6 space-y-6">
          {error && (
            <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              <AlertCircle className="h-4 w-4 shrink-0" />
              {error}
            </div>
          )}
          {success && (
            <div className="flex items-center gap-2 rounded-md border border-green-500/40 bg-green-50/30 px-3 py-2 text-sm text-green-700 dark:text-green-400">
              <CheckCircle className="h-4 w-4 shrink-0" />
              {success}
            </div>
          )}

          {loading ? (
            <div className="flex items-center justify-center py-20 gap-2 text-muted-foreground">
              <Loader2 className="h-5 w-5 animate-spin" />
              <span className="text-sm">{t('admin.loading')}</span>
            </div>
          ) : users.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-20 gap-3 text-center">
              <Users className="h-10 w-10 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">{t('admin.noUsers')}</p>
            </div>
          ) : (
            <div className="relative rounded-lg border border-border overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="sticky top-0 z-10 bg-muted/95 backdrop-blur-sm">
                  <tr className="border-b border-border text-muted-foreground">
                    <SortableTh
                      className="text-left px-3 py-2 font-medium w-full"
                      label={t('admin.colUser')}
                      active={usersSort.sortKey === 'user'}
                      dir={usersSort.sortDir}
                      onToggle={() => usersSort.toggleSort('user')}
                    />
                    <SortableTh
                      className="hidden md:table-cell text-left px-3 py-2 font-medium"
                      label={t('admin.colRole')}
                      active={usersSort.sortKey === 'role'}
                      dir={usersSort.sortDir}
                      onToggle={() => usersSort.toggleSort('role')}
                    />
                    <SortableTh
                      className="text-left px-3 py-2 font-medium"
                      label={t('admin.colStorage')}
                      active={usersSort.sortKey === 'storage'}
                      dir={usersSort.sortDir}
                      onToggle={() => usersSort.toggleSort('storage')}
                    />
                    <SortableTh
                      className="hidden lg:table-cell text-left px-3 py-2 font-medium"
                      label={t('admin.col2fa')}
                      active={usersSort.sortKey === 'twofa'}
                      dir={usersSort.sortDir}
                      onToggle={() => usersSort.toggleSort('twofa')}
                    />
                    <SortableTh
                      className="hidden lg:table-cell text-left px-3 py-2 font-medium"
                      label={t('admin.colStatus')}
                      active={usersSort.sortKey === 'status'}
                      dir={usersSort.sortDir}
                      onToggle={() => usersSort.toggleSort('status')}
                    />
                    <th className="px-3 py-2 w-10" />
                  </tr>
                </thead>
                <tbody>
                  {usersSort.sorted.map((user) => (
                    <UserRow
                      key={user.id}
                      user={user}
                      isSelf={user.id === selfID}
                      onToggleActive={handleToggleActive}
                      onToggleRole={handleToggleRole}
                      onSetQuota={setQuotaTarget}
                      onUnlock={handleUnlock}
                      onRecompute={handleRecompute}
                      togglingActive={togglingActive === user.id}
                      togglingRole={togglingRole === user.id}
                      unlocking={unlocking === user.id}
                      recomputing={recomputing === user.id}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      <CreateUserDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={(user) => {
          setUsers((prev) => [user, ...prev])
          flashSuccess(t('admin.msgUserCreated', { user: user.username }))
        }}
      />

      <QuotaDialog
        user={quotaTarget}
        onClose={() => setQuotaTarget(null)}
        onUpdated={(id, bytes) => {
          setUsers((prev) =>
            prev.map((u) => u.id === id ? { ...u, quota_bytes: bytes } : u)
          )
          flashSuccess(t('admin.msgQuotaUpdated'))
        }}
      />
    </div>
  )
}
