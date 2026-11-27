import { useCallback, useEffect, useState } from 'react'
import { AlertCircle, Loader2, Play, RefreshCw } from 'lucide-react'
import { Button } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { apiClient } from '../../store/auth'
import { useScrolled } from '../../hooks/useScrolled'
import { useTableSort } from '../../hooks/useTableSort'
import { SortableTh } from '../../components/SortableTh'
import type { AdminJob } from '@privasync/api-client'
import { PageHeader } from '../../components/PageHeader'

function statusRank(j: AdminJob): number {
  if (j.running) return 0
  if (j.status === 'error') return 1
  if (j.status === 'success') return 2
  return 3
}

function lastRunOf(j: AdminJob): string {
  return j.finished_at ?? j.started_at ?? ''
}

function formatTime(iso: string | null, locale: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return d.toLocaleString(locale)
}

function StatusBadge({ job, t }: { job: AdminJob; t: (k: string) => string }) {
  if (job.running) {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-md bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-800 dark:bg-blue-900/40 dark:text-blue-300">
        <Loader2 className="h-3 w-3 animate-spin" />
        {t('admin.jobs.statusRunning')}
      </span>
    )
  }
  if (job.status === 'success') {
    return (
      <span className="rounded-md bg-green-100 px-2 py-0.5 text-xs font-medium text-green-800 dark:bg-green-900/40 dark:text-green-300">
        {t('admin.jobs.statusSuccess')}
      </span>
    )
  }
  if (job.status === 'error') {
    return (
      <span className="rounded-md bg-red-100 px-2 py-0.5 text-xs font-medium text-red-800 dark:bg-red-900/40 dark:text-red-300">
        {t('admin.jobs.statusError')}
      </span>
    )
  }
  return (
    <span className="text-xs text-muted-foreground">{t('admin.jobs.neverRun')}</span>
  )
}

export default function AdminJobsPage() {
  const { t, i18n } = useTranslation()
  const locale = i18n.language === 'de' ? 'de-DE' : 'en-US'
  const { scrolled, onScroll } = useScrolled()
  const [jobs, setJobs] = useState<AdminJob[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [triggering, setTriggering] = useState<string | null>(null)
  const jobLabel = useCallback(
    (j: AdminJob) => t(`admin.jobs.labels.${j.id}`, j.label || j.id),
    [t],
  )
  const scheduleLabel = useCallback(
    (j: AdminJob) => j.schedule
      ? t(`admin.jobs.schedules.${j.schedule}`, j.schedule)
      : t('admin.jobs.scheduleManual'),
    [t],
  )
  const sort = useTableSort<AdminJob, 'job' | 'status' | 'lastRun' | 'schedule' | 'items'>(jobs, {
    storageKey: 'admin.jobs',
    comparators: {
      job:      (a, b) => jobLabel(a).localeCompare(jobLabel(b)),
      status:   (a, b) => statusRank(a) - statusRank(b),
      lastRun:  (a, b) => lastRunOf(a).localeCompare(lastRunOf(b)),
      schedule: (a, b) => scheduleLabel(a).localeCompare(scheduleLabel(b)),
      items:    (a, b) => a.items - b.items,
    },
    defaultSort: { key: 'job', dir: 'asc' },
  })

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await apiClient.admin.listJobs()
      setJobs(res.jobs)
    } catch {
      setError(t('admin.jobs.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { load() }, [load])

  // Poll while any job is running so the table reflects completion without
  // forcing the admin to hit refresh manually.
  useEffect(() => {
    if (!jobs.some(j => j.running)) return
    const id = window.setInterval(load, 2000)
    return () => window.clearInterval(id)
  }, [jobs, load])

  const trigger = useCallback(async (id: string) => {
    setTriggering(id)
    setError(null)
    try {
      await apiClient.admin.triggerJob(id)
      // Optimistic: mark as running immediately, then refresh.
      setJobs(prev => prev.map(j => j.id === id ? { ...j, running: true, status: 'running' } : j))
      await load()
    } catch (e: any) {
      const code = e?.code ?? e?.error?.code
      setError(code === 'JOB_RUNNING' ? t('admin.jobs.triggerBusy') : t('admin.jobs.triggerFailed'))
    } finally {
      setTriggering(null)
    }
  }, [load, t])

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title={t('admin.jobs.title')}
        subtitle={t('admin.jobs.subtitle')}
        scrolled={scrolled}
        actions={
          <Button size="sm" onClick={load} disabled={loading}>
            {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
            {t('admin.jobs.refresh')}
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

          {loading && jobs.length === 0 && (
            <div className="flex items-center justify-center py-20 gap-2 text-muted-foreground">
              <Loader2 className="h-5 w-5 animate-spin" />
              <span className="text-sm">{t('admin.jobs.loading')}</span>
            </div>
          )}

          {jobs.length > 0 && (
            <div className="relative rounded-lg border border-border overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="bg-muted/40">
                  <tr className="text-left text-xs font-medium text-muted-foreground">
                    <SortableTh className="px-4 py-2"            label={t('admin.jobs.colJob')}      active={sort.sortKey === 'job'}      dir={sort.sortDir} onToggle={() => sort.toggleSort('job')} />
                    <SortableTh className="px-4 py-2"            label={t('admin.jobs.colStatus')}   active={sort.sortKey === 'status'}   dir={sort.sortDir} onToggle={() => sort.toggleSort('status')} />
                    <SortableTh className="px-4 py-2"            label={t('admin.jobs.colLastRun')}  active={sort.sortKey === 'lastRun'}  dir={sort.sortDir} onToggle={() => sort.toggleSort('lastRun')} />
                    <SortableTh className="px-4 py-2"            label={t('admin.jobs.colSchedule')} active={sort.sortKey === 'schedule'} dir={sort.sortDir} onToggle={() => sort.toggleSort('schedule')} />
                    <SortableTh className="px-4 py-2 text-right" label={t('admin.jobs.colItems')}    active={sort.sortKey === 'items'}    dir={sort.sortDir} onToggle={() => sort.toggleSort('items')} align="right" />
                    <th className="px-4 py-2 text-right">{t('admin.jobs.colAction')}</th>
                  </tr>
                </thead>
                <tbody>
                  {sort.sorted.map(j => (
                    <tr key={j.id} className="border-t border-border align-top">
                      <td className="px-4 py-3">
                        <div className="font-medium">{t(`admin.jobs.labels.${j.id}`, j.label || j.id)}</div>
                        <div className="text-xs text-muted-foreground">{j.id}</div>
                        {j.status === 'error' && j.message && (
                          <div className="mt-1 text-xs text-destructive">{j.message}</div>
                        )}
                      </td>
                      <td className="px-4 py-3"><StatusBadge job={j} t={t} /></td>
                      <td className="px-4 py-3 tabular-nums text-muted-foreground">
                        {formatTime(j.finished_at ?? j.started_at, locale)}
                      </td>
                      <td className="px-4 py-3 text-muted-foreground">
                        {j.schedule
                          ? t(`admin.jobs.schedules.${j.schedule}`, j.schedule)
                          : t('admin.jobs.scheduleManual')}
                      </td>
                      <td className="px-4 py-3 tabular-nums text-right">{j.items}</td>
                      <td className="px-4 py-3 text-right">
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => trigger(j.id)}
                          disabled={j.running || triggering === j.id}
                        >
                          {(j.running || triggering === j.id)
                            ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            : <Play className="h-3.5 w-3.5" />}
                          {j.running ? t('admin.jobs.running') : t('admin.jobs.trigger')}
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
    </div>
  )
}
