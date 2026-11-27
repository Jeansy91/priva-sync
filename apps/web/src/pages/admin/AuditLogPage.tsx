import { useCallback, useEffect, useMemo, useState } from 'react'
import { AlertCircle, FileClock, Loader2 } from 'lucide-react'
import { Label, Button, Select, DateTimePicker, type SelectOption } from '@privasync/ui'
import { useScrolled } from '../../hooks/useScrolled'
import { useTableSort } from '../../hooks/useTableSort'
import { SortableTh } from '../../components/SortableTh'
import { useTranslation } from '@privasync/i18n'
import { apiClient } from '../../store/auth'
import type { AdminUser, AuditEvent, AuditLogListParams } from '@privasync/api-client'
import { PageHeader } from '../../components/PageHeader'

const PAGE_SIZE = 100

const EVENT_TYPES = [
  'auth.login.success',
  'auth.login.fail',
  'auth.login.lockout',
  'auth.logout',
  'auth.password_changed',
  'auth.password_reset',
  'auth.username_changed',
  'auth.email_change_requested',
  'auth.email_changed',
  'auth.2fa_enabled',
  'auth.2fa_disabled',
  'auth.token_refresh',
  'files.deleted',
  'shares.created',
  'shares.deleted',
  'admin.user_created',
  'admin.user_updated',
  'admin.user_deleted',
  'admin.quota_set',
  'admin.quota_recomputed',
  'admin.user_unlocked',
] as const

interface Filters {
  event:  string
  from:   string
  to:     string
  user:   string
  offset: number
}

// datetime-local erwartet "YYYY-MM-DDTHH:mm" in lokaler Zeit (ohne TZ-Suffix).
function toLocalInput(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function defaultFilters(): Filters {
  const now  = new Date()
  const from = new Date(now)
  from.setDate(from.getDate() - 7)
  from.setHours(0, 0, 0, 0)
  return { event: '', from: toLocalInput(from), to: toLocalInput(now), user: '', offset: 0 }
}

function toParams(f: Filters): AuditLogListParams {
  const p: AuditLogListParams = { limit: PAGE_SIZE, offset: f.offset }
  if (f.event) p.event = f.event
  if (f.from)  p.from  = new Date(f.from).toISOString()
  if (f.to)    p.to    = new Date(f.to).toISOString()
  if (f.user)  p.user  = f.user
  return p
}

export default function AuditLogPage() {
  const { t, i18n } = useTranslation()
  const localeTag = i18n.language === 'de' ? 'de-DE' : 'en-US'
  const { scrolled, onScroll } = useScrolled()
  const [events, setEvents]   = useState<AuditEvent[]>([])
  const [users, setUsers]     = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError]     = useState<string | null>(null)
  const [filters, setFilters] = useState<Filters>(defaultFilters)
  const [hasMore, setHasMore] = useState(false)

  const eventLabel = useCallback((ev: string) => t(`admin.auditLog.events.${ev}`, ev), [t])

  const userNameOf = useCallback((uid: string | null | undefined): string => {
    if (!uid) return ''
    return users.find((u) => u.id === uid)?.username ?? uid
  }, [users])

  const sort = useTableSort<AuditEvent, 'time' | 'event' | 'user' | 'ip'>(events, {
    storageKey: 'admin.auditLog',
    comparators: {
      time:  (a, b) => a.created_at.localeCompare(b.created_at),
      event: (a, b) => eventLabel(a.event_type).localeCompare(eventLabel(b.event_type)),
      user:  (a, b) => userNameOf(a.user_id).localeCompare(userNameOf(b.user_id)),
      ip:    (a, b) => (a.ip_address ?? '').localeCompare(b.ip_address ?? ''),
    },
    defaultSort: { key: 'time', dir: 'desc' },
  })

  const eventOptions = useMemo<SelectOption[]>(() => [
    { value: '', label: t('admin.auditLog.filterAll') },
    ...EVENT_TYPES.map((ev) => ({ value: ev, label: eventLabel(ev) })),
  ], [t, eventLabel])

  const userOptions = useMemo<SelectOption[]>(() => [
    { value: '', label: t('admin.auditLog.filterUserAll') },
    ...users.map((u) => ({ value: u.id, label: u.username })),
  ], [users, t])

  const load = useCallback(async (f: Filters, append: boolean) => {
    setLoading(true)
    setError(null)
    try {
      const r = await apiClient.admin.listAuditLog(toParams(f))
      const list = r.events ?? []
      setEvents((prev) => append ? [...prev, ...list] : list)
      setHasMore(list.length === PAGE_SIZE)
    } catch {
      setError(t('admin.auditLog.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { load(filters, filters.offset > 0) }, [filters, load])

  useEffect(() => {
    apiClient.admin.listUsers()
      .then((list) => setUsers(list ?? []))
      .catch(() => {})
  }, [])

  const update = (patch: Partial<Filters>) =>
    setFilters((prev) => ({ ...prev, ...patch, offset: 0 }))

  const loadMore = () =>
    setFilters((prev) => ({ ...prev, offset: prev.offset + PAGE_SIZE }))

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title={t('admin.auditLog.title')}
        subtitle={t('admin.auditLog.subtitle')}
        scrolled={scrolled}
      />

      <div className="flex-1 overflow-auto" onScroll={onScroll}>
        <div className="max-w-7xl mx-auto p-4 sm:p-6 space-y-4">
          <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-3">
            <div className="space-y-1">
              <Label htmlFor="filter-event">{t('admin.auditLog.colEvent')}</Label>
              <Select
                value={filters.event}
                onChange={(v) => update({ event: v })}
                options={eventOptions}
                placeholder={t('admin.auditLog.filterEventPlaceholder')}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="filter-from">{t('admin.auditLog.filterFrom')}</Label>
              <DateTimePicker
                id="filter-from"
                value={filters.from}
                onChange={(v) => update({ from: v })}
                locale={localeTag}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="filter-to">{t('admin.auditLog.filterTo')}</Label>
              <DateTimePicker
                id="filter-to"
                value={filters.to}
                onChange={(v) => update({ to: v })}
                locale={localeTag}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="filter-user">{t('admin.auditLog.filterUser')}</Label>
              <Select
                value={filters.user}
                onChange={(v) => update({ user: v })}
                options={userOptions}
                placeholder={t('admin.auditLog.filterUserPlaceholder')}
              />
            </div>
          </div>

          {error && (
            <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              <AlertCircle className="h-4 w-4 shrink-0" />
              {error}
            </div>
          )}

          {loading && events.length === 0 ? (
            <div className="flex items-center justify-center py-20 gap-2 text-muted-foreground">
              <Loader2 className="h-5 w-5 animate-spin" />
              <span className="text-sm">{t('admin.auditLog.loading')}</span>
            </div>
          ) : events.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-20 gap-3 text-center">
              <FileClock className="h-10 w-10 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">{t('admin.auditLog.empty')}</p>
            </div>
          ) : (
            <div className="relative rounded-lg border border-border overflow-x-auto">
              <table className="w-full min-w-[640px] text-sm">
                <thead className="sticky top-0 z-10 bg-muted/95 backdrop-blur-sm">
                  <tr className="border-b border-border text-muted-foreground">
                    <SortableTh className="text-left px-4 py-2.5 font-medium" label={t('admin.auditLog.colTime')}  active={sort.sortKey === 'time'}  dir={sort.sortDir} onToggle={() => sort.toggleSort('time')} />
                    <SortableTh className="text-left px-4 py-2.5 font-medium" label={t('admin.auditLog.colEvent')} active={sort.sortKey === 'event'} dir={sort.sortDir} onToggle={() => sort.toggleSort('event')} />
                    <SortableTh className="text-left px-4 py-2.5 font-medium" label={t('admin.auditLog.colUser')}  active={sort.sortKey === 'user'}  dir={sort.sortDir} onToggle={() => sort.toggleSort('user')} />
                    <SortableTh className="text-left px-4 py-2.5 font-medium" label={t('admin.auditLog.colIp')}    active={sort.sortKey === 'ip'}    dir={sort.sortDir} onToggle={() => sort.toggleSort('ip')} />
                  </tr>
                </thead>
                <tbody>
                  {sort.sorted.map((ev) => {
                    const userName = ev.user_id
                      ? users.find((u) => u.id === ev.user_id)?.username ?? ev.user_id
                      : '—'
                    return (
                      <tr key={ev.id} className="border-b border-border/50 hover:bg-accent/20 transition-colors">
                        <td className="px-4 py-2 text-xs text-muted-foreground whitespace-nowrap">
                          {new Date(ev.created_at).toLocaleString()}
                        </td>
                        <td className="px-4 py-2 text-xs">
                          <div>{eventLabel(ev.event_type)}</div>
                          <div className="font-mono text-[10px] text-muted-foreground/80">{ev.event_type}</div>
                        </td>
                        <td className="px-4 py-2 text-xs text-muted-foreground">
                          {userName}
                        </td>
                        <td className="px-4 py-2 text-xs text-muted-foreground">
                          {ev.ip_address || '—'}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}

          {hasMore && !loading && (
            <div className="flex justify-center pt-2">
              <Button variant="outline" onClick={loadMore}>
                {t('admin.auditLog.loadMore')}
              </Button>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
