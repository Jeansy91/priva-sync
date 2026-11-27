import {
  Button, cn, DropdownMenu, type DropdownMenuItem,
} from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import {
  Loader2, ShieldCheck, Shield, Ban, UserCheck, HardDrive,
  KeyRound, RefreshCw, ShieldOff,
} from 'lucide-react'
import type { AdminUser } from '@privasync/api-client'
import { UserAvatar } from '../../components/layout/AppShell'
import { TitleWrap } from '../../components/TitleWrap'
import { formatBytes } from './formatBytes'

interface UserRowProps {
  user:           AdminUser
  isSelf:         boolean
  onToggleActive: (u: AdminUser) => void
  onToggleRole:   (u: AdminUser) => void
  onSetQuota:     (u: AdminUser) => void
  onUnlock:       (u: AdminUser) => void
  onRecompute:    (u: AdminUser) => void
  togglingActive: boolean
  togglingRole:   boolean
  unlocking:      boolean
  recomputing:    boolean
}

/**
 * One user row in the admin user table. Hosts:
 *   - identity column (avatar + name + email + responsive pills)
 *   - role / storage / 2FA / status cells, hidden at smaller breakpoints
 *   - action column that toggles between inline icon buttons (xl) and a
 *     dropdown kebab (below xl)
 *
 * Self-aware: demoting / locking your own account is disabled, and the
 * row labels switch to the "self" variants for clearer wording.
 */
export function UserRow(props: UserRowProps) {
  const {
    user, isSelf,
    onToggleActive, onToggleRole, onSetQuota, onUnlock, onRecompute,
    togglingActive, togglingRole, unlocking, recomputing,
  } = props
  const { t } = useTranslation()
  const usedPct =
    user.quota_bytes > 0
      ? Math.min(100, Math.round((user.used_bytes / user.quota_bytes) * 100))
      : null

  const roleLabel = user.role === 'admin'
    ? (isSelf ? t('admin.actionDemoteSelf') : t('admin.actionDemote'))
    : t('admin.actionPromote')
  const lockLabel = isSelf && user.is_active
    ? t('admin.actionLockSelf')
    : user.is_active ? t('admin.actionLock') : t('admin.actionUnlock')

  const menuItems: DropdownMenuItem[] = [
    {
      label:    t('admin.actionQuota'),
      icon:     <HardDrive className="h-4 w-4" />,
      onSelect: () => onSetQuota(user),
    },
    {
      label:    t('admin.actionRecompute'),
      icon:     recomputing ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />,
      onSelect: () => onRecompute(user),
      disabled: recomputing,
    },
    {
      label:    user.is_locked ? t('admin.actionUnlockLocked') : t('admin.actionUnlockUnlocked'),
      icon:     unlocking ? <Loader2 className="h-4 w-4 animate-spin" /> : <KeyRound className={cn('h-4 w-4', user.is_locked && 'text-destructive')} />,
      onSelect: () => onUnlock(user),
      disabled: unlocking || !user.is_locked,
    },
    {
      label:    roleLabel,
      icon:     togglingRole
        ? <Loader2 className="h-4 w-4 animate-spin" />
        : user.role === 'admin'
          ? <ShieldOff className="h-4 w-4" />
          : <ShieldCheck className="h-4 w-4" />,
      onSelect: () => onToggleRole(user),
      disabled: togglingRole || (isSelf && user.role === 'admin'),
    },
    {
      label:    lockLabel,
      icon:     togglingActive
        ? <Loader2 className="h-4 w-4 animate-spin" />
        : user.is_active
          ? <Ban className="h-4 w-4" />
          : <UserCheck className="h-4 w-4" />,
      onSelect: () => onToggleActive(user),
      disabled: togglingActive || (isSelf && user.is_active),
      variant:  user.is_active && !isSelf ? 'destructive' : 'default',
    },
  ]

  return (
    <tr className="border-b border-border/50 hover:bg-accent/20 transition-colors">
      {/* max-w-0 + the header's w-full: long e-mails truncate instead of
          widening the table past a phone screen. */}
      <td className="px-3 py-2 max-w-0">
        <div className="flex items-center gap-2">
          <UserAvatar user={user} size={32} />
          <div className="min-w-0">
            <p className="text-sm font-medium truncate">{user.username}</p>
            <p className="text-xs text-muted-foreground truncate">{user.email}</p>
            {/* Pills nur unterhalb md sichtbar — ab md eigene Spalten */}
            <div className="md:hidden flex flex-wrap items-center gap-1 pt-0.5">
              <span className={cn(
                'inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-[10px] font-medium',
                user.role === 'admin'
                  ? 'bg-primary/10 text-primary'
                  : 'bg-muted text-muted-foreground',
              )}>
                {user.role === 'admin'
                  ? <ShieldCheck className="h-2.5 w-2.5" />
                  : <Shield className="h-2.5 w-2.5" />}
                {user.role === 'admin' ? t('admin.roleAdmin') : t('admin.roleUser')}
              </span>
              {user.totp_enabled && (
                <span className="inline-flex items-center gap-1 rounded-full bg-green-100 px-1.5 py-0.5 text-[10px] font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
                  <ShieldCheck className="h-2.5 w-2.5" />
                  {t('admin.totpActive')}
                </span>
              )}
              {!user.is_active && (
                <span className="inline-flex items-center rounded-full bg-red-100 px-1.5 py-0.5 text-[10px] font-medium text-red-700 dark:bg-red-900/30 dark:text-red-400">
                  {t('admin.statusLocked')}
                </span>
              )}
            </div>
          </div>
        </div>
      </td>
      <td className="hidden md:table-cell px-3 py-2">
        <div className="flex items-center gap-1.5 text-xs whitespace-nowrap">
          {user.role === 'admin'
            ? <><ShieldCheck className="h-3.5 w-3.5 text-primary" /><span className="font-medium text-primary">{t('admin.roleAdmin')}</span></>
            : <><Shield className="h-3.5 w-3.5 text-muted-foreground" /><span className="text-muted-foreground">{t('admin.roleUser')}</span></>}
        </div>
      </td>
      <td className="px-3 py-2">
        <div className="space-y-1 min-w-28">
          <p className="text-xs text-muted-foreground whitespace-nowrap">
            {formatBytes(user.used_bytes)}
            {user.quota_bytes > 0 && ` / ${formatBytes(user.quota_bytes)}`}
            {user.quota_bytes === 0 && ' / ∞'}
          </p>
          {usedPct !== null && (
            <div className="h-1 rounded-full bg-secondary overflow-hidden w-20">
              <div
                className={cn('h-full rounded-full', usedPct >= 90 ? 'bg-destructive' : 'bg-primary')}
                style={{ width: `${usedPct}%` }}
              />
            </div>
          )}
          {user.trash_bytes > 0 && (
            <p className="text-[10px] text-muted-foreground/80 whitespace-nowrap">
              {t('admin.trashLabel')}: {formatBytes(user.trash_bytes)}
            </p>
          )}
        </div>
      </td>
      <td className="hidden lg:table-cell px-3 py-2">
        <div className="flex items-center gap-1.5 text-xs whitespace-nowrap">
          {user.totp_enabled
            ? <><ShieldCheck className="h-3.5 w-3.5 text-green-600" /><span className="text-green-600">{t('admin.totpActive')}</span></>
            : <><Shield className="h-3.5 w-3.5 text-muted-foreground" /><span className="text-muted-foreground">{t('admin.totpOff')}</span></>}
        </div>
      </td>
      <td className="hidden lg:table-cell px-3 py-2">
        <span className={cn(
          'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap',
          user.is_active
            ? 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400'
            : 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
        )}>
          {user.is_active ? t('admin.statusActive') : t('admin.statusLocked')}
        </span>
      </td>
      <td className="px-3 py-2 text-right">
        {/* Inline-Buttons ab xl, sonst Kebab */}
        <div className="hidden xl:flex items-center justify-end gap-1">
          {menuItems.map((it) => (
            <TitleWrap key={it.label} title={it.label}>
              <Button
                variant="ghost"
                size="icon"
                className={cn(
                  'h-7 w-7',
                  it.variant === 'destructive' && !it.disabled && 'text-destructive hover:text-destructive',
                )}
                aria-label={it.label}
                onClick={() => { if (!it.disabled) it.onSelect() }}
                disabled={it.disabled}
              >
                {it.icon}
              </Button>
            </TitleWrap>
          ))}
        </div>
        <div className="xl:hidden">
          <DropdownMenu
            triggerLabel={t('admin.actionsMenu')}
            items={menuItems}
          />
        </div>
      </td>
    </tr>
  )
}
