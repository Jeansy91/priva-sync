import { useCallback, useEffect, useState } from 'react'
import { AlertCircle, Loader2, RefreshCw, Trash2 } from 'lucide-react'
import { Button } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { apiClient } from '../../store/auth'
import { useScrolled } from '../../hooks/useScrolled'
import { useTableSort } from '../../hooks/useTableSort'
import { SortableTh } from '../../components/SortableTh'
import { PageHeader } from '../../components/PageHeader'
import { DeleteClientDialog } from './DeleteClientDialog'
import type { AdminClient } from '@privasync/api-client'

function formatTime(iso: string | null, locale: string, never: string): string {
  if (!iso) return never
  return new Date(iso).toLocaleString(locale)
}

export default function AdminClientsPage() {
  const { t, i18n } = useTranslation()
  const locale = i18n.language === 'de' ? 'de-DE' : 'en-US'
  const { scrolled, onScroll } = useScrolled()
  const [clients, setClients] = useState<AdminClient[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminClient | null>(null)
  const sort = useTableSort<AdminClient, 'user' | 'name' | 'platform' | 'osVersion' | 'clientVersion' | 'ip' | 'lastSeen'>(clients, {
    storageKey: 'admin.clients',
    comparators: {
      user:     (a, b) => a.username.localeCompare(b.username),
      name:     (a, b) => a.name.localeCompare(b.name),
      platform: (a, b) => a.platform.localeCompare(b.platform),
      osVersion: (a, b) => (a.os_version ?? '').localeCompare(b.os_version ?? ''),
      clientVersion: (a, b) => (a.client_version ?? '').localeCompare(b.client_version ?? ''),
      ip:       (a, b) => (a.last_ip ?? '').localeCompare(b.last_ip ?? ''),
      lastSeen: (a, b) => (a.last_seen ?? '').localeCompare(b.last_seen ?? ''),
    },
    defaultSort: { key: 'lastSeen', dir: 'desc' },
  })

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await apiClient.admin.listClients()
      setClients(res.clients)
    } catch {
      setError(t('admin.clients.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { load() }, [load])

  const never = t('admin.clients.never')

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title={t('admin.clients.title')}
        subtitle={t('admin.clients.subtitle')}
        scrolled={scrolled}
        actions={
          <Button size="sm" onClick={load} disabled={loading}>
            {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
            {t('admin.clients.refresh')}
          </Button>
        }
      />

      <div className="flex-1 overflow-auto" onScroll={onScroll}>
        <div className="max-w-7xl mx-auto p-4 sm:p-6 space-y-4">
          {error && (
            <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              <AlertCircle className="h-4 w-4 shrink-0" />
              {error}
            </div>
          )}

          {loading && clients.length === 0 && (
            <div className="flex items-center justify-center py-20 gap-2 text-muted-foreground">
              <Loader2 className="h-5 w-5 animate-spin" />
              <span className="text-sm">{t('admin.clients.loading')}</span>
            </div>
          )}

          {!loading && clients.length === 0 && !error && (
            <div className="text-center py-20 text-sm text-muted-foreground">
              {t('admin.clients.empty')}
            </div>
          )}

          {clients.length > 0 && (
            <div className="relative rounded-lg border border-border overflow-x-auto">
              <table className="w-full text-sm whitespace-nowrap">
                <thead className="bg-muted/40">
                  <tr className="text-left text-xs font-medium text-muted-foreground">
                    <SortableTh className="px-4 py-2" label={t('admin.clients.colUser')}          active={sort.sortKey === 'user'}          dir={sort.sortDir} onToggle={() => sort.toggleSort('user')} />
                    <SortableTh className="px-4 py-2" label={t('admin.clients.colName')}          active={sort.sortKey === 'name'}          dir={sort.sortDir} onToggle={() => sort.toggleSort('name')} />
                    <SortableTh className="px-4 py-2" label={t('admin.clients.colPlatform')}      active={sort.sortKey === 'platform'}      dir={sort.sortDir} onToggle={() => sort.toggleSort('platform')} />
                    <SortableTh className="px-4 py-2" label={t('admin.clients.colOsVersion')}     active={sort.sortKey === 'osVersion'}     dir={sort.sortDir} onToggle={() => sort.toggleSort('osVersion')} />
                    <SortableTh className="px-4 py-2" label={t('admin.clients.colClientVersion')} active={sort.sortKey === 'clientVersion'} dir={sort.sortDir} onToggle={() => sort.toggleSort('clientVersion')} />
                    <SortableTh className="px-4 py-2" label={t('admin.clients.colIp')}            active={sort.sortKey === 'ip'}            dir={sort.sortDir} onToggle={() => sort.toggleSort('ip')} />
                    <SortableTh className="px-4 py-2" label={t('admin.clients.colLastSeen')}      active={sort.sortKey === 'lastSeen'}      dir={sort.sortDir} onToggle={() => sort.toggleSort('lastSeen')} />
                    <th className="px-4 py-2 w-px">
                      <span className="sr-only">{t('admin.clients.colActions')}</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {sort.sorted.map(c => (
                    <tr key={c.id} className="border-t border-border align-top">
                      <td className="px-4 py-3 font-medium">{c.username}</td>
                      <td className="px-4 py-3">{c.name}</td>
                      <td className="px-4 py-3">{t(`admin.clients.platforms.${c.platform}`, c.platform)}</td>
                      <td className="px-4 py-3 tabular-nums text-muted-foreground">{c.os_version ?? '—'}</td>
                      <td className="px-4 py-3 tabular-nums text-muted-foreground">{c.client_version ?? '—'}</td>
                      <td className="px-4 py-3 tabular-nums text-muted-foreground">{c.last_ip ?? '—'}</td>
                      <td className="px-4 py-3 tabular-nums text-muted-foreground">{formatTime(c.last_seen, locale, never)}</td>
                      <td className="px-4 py-3">
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={t('admin.clients.deleteAria', { name: c.name })}
                          onClick={() => setDeleteTarget(c)}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      <DeleteClientDialog
        target={deleteTarget}
        onCancel={() => setDeleteTarget(null)}
        onDeleted={load}
      />
    </div>
  )
}
