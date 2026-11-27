import { useCallback, useEffect, useState, type ReactNode } from 'react'
import {
  AlertCircle, Loader2, RefreshCw,
  Users, Files, FolderTree, Share2, HardDrive, Activity, ShieldAlert, Server,
} from 'lucide-react'
import { Button, cn } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { apiClient } from '../../store/auth'
import { useScrolled } from '../../hooks/useScrolled'
import type { AdminStats } from '@privasync/api-client'
import { PageHeader } from '../../components/PageHeader'

function formatBytes(b: number, locale: string): string {
  if (b <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(b) / Math.log(1024)))
  const v = b / Math.pow(1024, i)
  return `${v.toLocaleString(locale, { maximumFractionDigits: 1 })} ${units[i]}`
}

function formatUptime(seconds: number, t: (k: string, p?: any) => string): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return t('adminStats.uptimeDH', { d, h })
  if (h > 0) return t('adminStats.uptimeHM', { h, m })
  return t('adminStats.uptimeM', { m })
}

interface CardProps {
  icon: ReactNode
  title: string
  children: ReactNode
}

function Card({ icon, title, children }: CardProps) {
  return (
    <div className="rounded-xl border border-border bg-card p-5 space-y-4 shadow-xs">
      <div className="flex items-center gap-3">
        <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-muted text-muted-foreground shrink-0">
          {icon}
        </div>
        <h3 className="font-semibold leading-tight">{title}</h3>
      </div>
      <div className="space-y-2">{children}</div>
    </div>
  )
}

function Row({ label, value, accent }: { label: string; value: ReactNode; accent?: 'warn' | 'danger' | 'ok' }) {
  return (
    <div className="flex items-baseline justify-between gap-3 text-sm">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="flex items-center gap-2">
        {accent && (
          <span className={cn(
            'h-1.5 w-1.5 rounded-full',
            accent === 'warn'   && 'bg-amber-500',
            accent === 'danger' && 'bg-destructive',
            accent === 'ok'     && 'bg-green-500',
          )} />
        )}
        <span className={cn(
          'font-semibold tabular-nums',
          accent === 'warn'   && 'text-amber-600 dark:text-amber-400',
          accent === 'danger' && 'text-destructive',
          accent === 'ok'     && 'text-green-600 dark:text-green-400',
        )}>{value}</span>
      </span>
    </div>
  )
}

function ProgressBar({ percent, accent }: { percent: number; accent?: 'warn' | 'danger' }) {
  return (
    <div className="h-1.5 w-full rounded-full bg-muted overflow-hidden">
      <div
        className={cn(
          'h-full transition-all',
          accent === 'danger' ? 'bg-destructive'
            : accent === 'warn' ? 'bg-amber-500'
              : 'bg-primary',
        )}
        style={{ width: `${percent}%` }}
      />
    </div>
  )
}

export default function AdminStatsPage() {
  const { t, i18n } = useTranslation()
  const locale = i18n.language || 'de'
  const { scrolled, onScroll } = useScrolled()
  const [stats, setStats] = useState<AdminStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const s = await apiClient.admin.getStats()
      setStats(s)
    } catch {
      setError(t('adminStats.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { load() }, [load])

  const fmt = (n: number) => n.toLocaleString(locale)
  const bytes = (n: number) => formatBytes(n, locale)

  const diskFreePct = stats && stats.storage.disk_total_bytes > 0
    ? Math.round(100 * stats.storage.disk_free_bytes / stats.storage.disk_total_bytes)
    : null
  const diskUsedPct = diskFreePct !== null ? 100 - diskFreePct : null
  const diskAccent: 'warn' | 'danger' | undefined =
    diskFreePct !== null && diskFreePct < 10 ? 'danger'
      : diskFreePct !== null && diskFreePct < 25 ? 'warn'
        : undefined

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title={t('adminStats.title')}
        subtitle={t('adminStats.subtitle')}
        scrolled={scrolled}
        actions={
          <Button size="sm" onClick={load} disabled={loading}>
            {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
            {t('adminStats.refresh')}
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

          {!stats && loading && (
            <div className="flex items-center justify-center py-20 gap-2 text-muted-foreground">
              <Loader2 className="h-5 w-5 animate-spin" />
              <span className="text-sm">{t('adminStats.loading')}</span>
            </div>
          )}

          {stats && (
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              <Card icon={<Server className="h-4 w-4" />} title={t('adminStats.cards.system')}>
                <Row label={t('adminStats.fields.version')} value={stats.version} />
                <Row label={t('adminStats.fields.uptime')}  value={formatUptime(stats.health.uptime_seconds, t)} />
                <Row label={t('adminStats.fields.dbSize')}  value={bytes(stats.health.db_size_bytes)} />
                <Row
                  label={t('adminStats.fields.outboxPending')}
                  value={fmt(stats.health.outbox_pending)}
                  accent={stats.health.outbox_pending > 0 ? 'warn' : 'ok'}
                />
              </Card>

              <Card icon={<Users className="h-4 w-4" />} title={t('adminStats.cards.users')}>
                <Row label={t('adminStats.fields.usersTotal')}  value={fmt(stats.users.total)} />
                <Row label={t('adminStats.fields.usersActive')} value={fmt(stats.users.active)} />
                <Row label={t('adminStats.fields.usersAdmin')}  value={fmt(stats.users.admin)} />
                <Row label={t('adminStats.fields.usersTotp')}   value={fmt(stats.users.totp)} />
                <Row
                  label={t('adminStats.fields.usersLocked')}
                  value={fmt(stats.users.locked)}
                  accent={stats.users.locked > 0 ? 'danger' : undefined}
                />
              </Card>

              <Card icon={<FolderTree className="h-4 w-4" />} title={t('adminStats.cards.syncRoots')}>
                <Row label={t('adminStats.fields.syncRootsTotal')} value={fmt(stats.sync_roots.total)} />
              </Card>

              <Card icon={<Files className="h-4 w-4" />} title={t('adminStats.cards.files')}>
                <Row label={t('adminStats.fields.filesLive')}      value={fmt(stats.files.live)} />
                <Row label={t('adminStats.fields.filesTrash')}     value={fmt(stats.files.trash)} />
                <Row
                  label={t('adminStats.fields.filesConflicts')}
                  value={fmt(stats.files.conflicts)}
                  accent={stats.files.conflicts > 0 ? 'warn' : undefined}
                />
              </Card>

              <Card icon={<Share2 className="h-4 w-4" />} title={t('adminStats.cards.shares')}>
                <Row label={t('adminStats.fields.sharesTotal')}   value={fmt(stats.shares.total)} />
                <Row label={t('adminStats.fields.sharesActive')}  value={fmt(stats.shares.active)} />
                <Row label={t('adminStats.fields.sharesPwd')}     value={fmt(stats.shares.password_protected)} />
              </Card>

              <Card icon={<HardDrive className="h-4 w-4" />} title={t('adminStats.cards.storage')}>
                <Row label={t('adminStats.fields.storageUsed')}  value={bytes(stats.storage.used_bytes)} />
                <Row label={t('adminStats.fields.storageTrash')} value={bytes(stats.storage.trash_bytes)} />
                <Row label={t('adminStats.fields.storageQuota')} value={bytes(stats.storage.quota_bytes)} />
                <Row
                  label={t('adminStats.fields.diskFree')}
                  value={`${bytes(stats.storage.disk_free_bytes)}${diskFreePct !== null ? ` (${diskFreePct}%)` : ''}`}
                  accent={diskAccent}
                />
                <Row label={t('adminStats.fields.diskTotal')} value={bytes(stats.storage.disk_total_bytes)} />
                {diskUsedPct !== null && (
                  <div className="pt-2">
                    <ProgressBar percent={diskUsedPct} accent={diskAccent} />
                  </div>
                )}
              </Card>

              <Card icon={<ShieldAlert className="h-4 w-4" />} title={t('adminStats.cards.quotaHealth')}>
                <Row
                  label={t('adminStats.fields.warning80')}
                  value={fmt(stats.quota_health.warning80)}
                  accent={stats.quota_health.warning80 > 0 ? 'warn' : undefined}
                />
                <Row
                  label={t('adminStats.fields.critical95')}
                  value={fmt(stats.quota_health.critical95)}
                  accent={stats.quota_health.critical95 > 0 ? 'danger' : undefined}
                />
              </Card>

              <Card icon={<Activity className="h-4 w-4" />} title={t('adminStats.cards.activity24h')}>
                <Row label={t('adminStats.fields.logins')}       value={fmt(stats.activity_24h.logins)} />
                <Row
                  label={t('adminStats.fields.failedLogins')}
                  value={fmt(stats.activity_24h.failed_logins)}
                  accent={stats.activity_24h.failed_logins > 0 ? 'warn' : undefined}
                />
              </Card>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
