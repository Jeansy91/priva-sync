import { useEffect, useMemo, useState } from 'react'
import { useParams, Link as RouterLink } from 'react-router-dom'
import { AlertCircle, Folder, Download, Loader2, ChevronRight, Home } from 'lucide-react'
import { FileIcon } from '@privasync/ui'
import { Button, cn } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { apiClient } from '../store/auth'
import type { PublicShareView } from '@privasync/api-client'
import { useScrolled } from '../hooks/useScrolled'
import { PageHeader } from '../components/PageHeader'

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
}

export default function PublicSharePage() {
  const { t } = useTranslation()
  const { token } = useParams<{ token: string }>()
  const { scrolled, onScroll } = useScrolled()
  const [sub, setSub]     = useState('')
  const [view, setView]   = useState<PublicShareView | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!token) return
    setLoading(true)
    setError(null)
    apiClient.shares.publicEntries(token, sub || undefined)
      .then(setView)
      .catch(() => setError(t('shares.publicLoadFailed')))
      .finally(() => setLoading(false))
  }, [token, sub, t])

  // Directories first, each block sorted by name — same order as the
  // authenticated file browser.
  const sortedEntries = useMemo(() => {
    const entries = view?.entries ?? []
    return [...entries].sort((a, b) => {
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
      return a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true })
    })
  }, [view])

  if (!token) return null

  const segments = view && view.path !== '/' && !view.is_file
    ? view.path.split('/').filter(Boolean)
    : []

  if (loading && !view) {
    return (
      <div className="flex items-center justify-center h-dvh bg-background">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    )
  }

  return (
    <div className="flex flex-col h-dvh bg-background">
      <PageHeader
        title={t('shares.publicTitle')}
        subtitle={t('shares.publicSubtitle')}
        scrolled={scrolled}
        maxWidth="max-w-4xl"
        actions={view && !view.is_file && (
          <Button asChild size="sm">
            <a href={apiClient.shares.publicDownloadURL(token, sub || undefined)}>
              <Download className="h-3.5 w-3.5" />
              {t('shares.publicDownloadAll')}
            </a>
          </Button>
        )}
      />

      <main className="flex-1 overflow-auto" onScroll={onScroll}>
        {view && !view.is_file && (
          <nav className={cn(
            'sticky top-0 z-10 bg-background/95 backdrop-blur-sm transition-shadow',
            scrolled && 'border-b border-border',
          )}>
            <div className="max-w-4xl mx-auto px-4 sm:px-6 py-2 flex items-center gap-1 text-xs text-muted-foreground min-w-0">
              <button onClick={() => setSub('')} className="hover:text-foreground shrink-0">
                <Home className="h-3.5 w-3.5" />
              </button>
              {segments.map((seg, i) => {
                const rel = segments.slice(view.path.split('/').filter(Boolean).length, i + 1).join('/')
                return (
                  <span key={i} className="flex items-center gap-1 min-w-0">
                    <ChevronRight className="h-3 w-3 shrink-0" />
                    <button onClick={() => setSub(rel)} className="hover:text-foreground truncate">{seg}</button>
                  </span>
                )
              })}
            </div>
          </nav>
        )}
        <div className="max-w-4xl mx-auto px-4 sm:px-6 py-4 sm:py-6">
        {error && (
          <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            <AlertCircle className="h-4 w-4 shrink-0" />
            {error}
          </div>
        )}

        {view && view.is_file && (
          <div className="rounded-lg border border-border bg-card p-5 flex items-center gap-4">
            <FileIcon name={view.entries[0]?.name ?? view.path} className="h-10 w-10" />
            <div className="flex-1 min-w-0">
              <p className="font-medium truncate">{view.entries[0]?.name ?? view.path}</p>
              <p className="text-xs text-muted-foreground">
                {formatSize(view.entries[0]?.size ?? 0)}
              </p>
            </div>
            <a
              href={apiClient.shares.publicDownloadURL(token)}
              className="inline-flex items-center gap-2 rounded-md bg-primary text-primary-foreground px-3 py-2 text-sm hover:bg-primary/90"
            >
              <Download className="h-4 w-4" />
              {t('shares.publicDownload')}
            </a>
          </div>
        )}

        {view && !view.is_file && (
          <div className="space-y-3">
            {view.entries.length === 0 ? (
              <p className="py-10 text-center text-sm text-muted-foreground">
                {t('shares.publicEmpty')}
              </p>
            ) : (
              <ul className="rounded-lg border border-border bg-card divide-y divide-border">
                {sortedEntries.map((e) => {
                  const relFromRoot = e.path.startsWith(view.path)
                    ? e.path.slice(view.path.length).replace(/^\//, '')
                    : e.name
                  return (
                    <li key={e.path} className="flex items-center gap-3 px-4 py-2.5">
                      {e.is_dir
                        ? <Folder className="h-4 w-4 text-blue-500 shrink-0" />
                        : <FileIcon name={e.name} className="h-4 w-4" />}
                      <span className="flex-1 truncate text-sm">
                        {e.is_dir
                          ? (
                            <button
                              className="hover:underline text-left w-full truncate"
                              onClick={() => setSub((prev) => prev ? `${prev}/${e.name}` : e.name)}
                            >
                              {e.name}
                            </button>
                          )
                          : e.name}
                      </span>
                      {!e.is_dir && (
                        <span className="text-xs text-muted-foreground shrink-0">{formatSize(e.size)}</span>
                      )}
                      <a
                        href={apiClient.shares.publicDownloadURL(token, relFromRoot)}
                        className="text-muted-foreground hover:text-foreground shrink-0"
                        aria-label={e.is_dir ? t('shares.publicDownloadFolder') : t('shares.publicDownload')}
                      >
                        <Download className="h-4 w-4" />
                      </a>
                    </li>
                  )
                })}
              </ul>
            )}
          </div>
        )}

          <p className="text-xs text-muted-foreground mt-8 text-center">
            <RouterLink to="/" className="hover:underline">PrivaSync</RouterLink>
          </p>
        </div>
      </main>
    </div>
  )
}
