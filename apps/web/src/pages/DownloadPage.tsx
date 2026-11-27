import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Apple, ArrowLeft, Download, ExternalLink, Loader2, Monitor, Terminal } from 'lucide-react'
import { Button } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { useAuthStore } from '../store/auth'
import { PageHeader } from '../components/PageHeader'

type Target = 'windows-x86_64' | 'darwin-x86_64' | 'darwin-aarch64' | 'linux-x86_64'

const TARGETS: Target[] = ['windows-x86_64', 'darwin-aarch64', 'darwin-x86_64', 'linux-x86_64']

interface DownloadEntry {
  target: string
  file:   string
  url:    string
}

// Response of GET /api/v1/client/downloads (public).
interface DownloadsResponse {
  version:     string
  release_url: string
  downloads:   DownloadEntry[]
}

function detectTarget(): Target {
  const ua   = navigator.userAgent
  const plat = navigator.platform || ''

  if (/Win/i.test(plat) || /Windows/i.test(ua)) return 'windows-x86_64'
  if (/Mac/i.test(plat)) {
    // The user agent does not reveal Apple Silicon reliably; default to it
    // unless the UA names Intel explicitly, since current Macs are arm64.
    if (/Intel/i.test(ua) && !/ARM|Apple Silicon/i.test(ua)) return 'darwin-x86_64'
    return 'darwin-aarch64'
  }
  return 'linux-x86_64'
}

const TARGET_LABEL_KEYS: Record<Target, string> = {
  'windows-x86_64': 'downloadPage.platform.windows',
  'darwin-aarch64': 'downloadPage.platform.macosArm',
  'darwin-x86_64':  'downloadPage.platform.macosIntel',
  'linux-x86_64':   'downloadPage.platform.linux',
}

const TARGET_HINT_KEYS: Record<Target, string> = {
  'windows-x86_64': 'downloadPage.hint.windows',
  'darwin-aarch64': 'downloadPage.hint.macos',
  'darwin-x86_64':  'downloadPage.hint.macos',
  'linux-x86_64':   'downloadPage.hint.linux',
}

function TargetIcon({ target }: { target: Target }) {
  if (target.startsWith('windows')) return <Monitor className="h-4 w-4" />
  if (target.startsWith('darwin'))  return <Apple   className="h-4 w-4" />
  return <Terminal className="h-4 w-4" />
}

function isTarget(t: string): t is Target {
  return (TARGETS as string[]).includes(t)
}

export default function DownloadPage() {
  const { t }               = useTranslation()
  const isAuthenticated     = useAuthStore((s) => s.isAuthenticated)
  const detected            = detectTarget()
  const [data, setData]     = useState<DownloadsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError]   = useState(false)

  useEffect(() => {
    const ctrl = new AbortController()
    ;(async () => {
      try {
        const res = await fetch('/api/v1/client/downloads', { signal: ctrl.signal })
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        setData(await res.json() as DownloadsResponse)
      } catch (err) {
        if ((err as Error).name === 'AbortError') return
        setError(true)
      } finally {
        if (!ctrl.signal.aborted) setLoading(false)
      }
    })()
    return () => ctrl.abort()
  }, [])

  const entries = (data?.downloads ?? []).filter((d) => isTarget(d.target))
  const primary = entries.find((d) => d.target === detected)
  const others  = entries.filter((d) => d !== primary)

  return (
    <div className="min-h-screen bg-background">
      <div className="mx-auto max-w-4xl px-4 pt-8 sm:px-6">
        <Link
          to={isAuthenticated ? '/files' : '/login'}
          className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          {isAuthenticated ? t('downloadPage.back') : t('downloadPage.backToLogin')}
        </Link>
      </div>
      <PageHeader
        title={t('downloadPage.title')}
        subtitle={t('downloadPage.subtitle')}
        scrolled={false}
        maxWidth="max-w-4xl"
      />

      <div className="mx-auto max-w-4xl space-y-6 px-4 pb-12 sm:px-6">
        {loading && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" />
            {t('downloadPage.loading')}
          </div>
        )}

        {error && (
          <div className="rounded-md border border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive">
            {t('downloadPage.loadError')}
          </div>
        )}

        {!loading && !error && entries.length === 0 && (
          <div className="rounded-md border border-border bg-muted/30 px-4 py-8 text-center text-sm text-muted-foreground">
            {t('downloadPage.empty')}
          </div>
        )}

        {data && entries.length > 0 && (
          <div className="space-y-4">
            {primary && (
              <div className="space-y-3 rounded-lg border border-border bg-card p-6">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <p className="text-xs uppercase tracking-wide text-muted-foreground">
                      {t('downloadPage.recommended')}
                    </p>
                    <p className="mt-1 flex items-center gap-2 text-base font-medium">
                      <TargetIcon target={detected} />
                      {t(TARGET_LABEL_KEYS[detected])}
                    </p>
                  </div>
                  <Button asChild size="lg">
                    <a href={primary.url} download>
                      <Download className="h-4 w-4" />
                      {t('downloadPage.downloadVersion', { version: data.version })}
                    </a>
                  </Button>
                </div>
                <p className="text-sm text-muted-foreground">{t(TARGET_HINT_KEYS[detected])}</p>
              </div>
            )}

            {others.length > 0 && (
              <div className="space-y-3 rounded-lg border border-border bg-card p-4">
                <p className="text-xs uppercase tracking-wide text-muted-foreground">
                  {primary ? t('downloadPage.otherPlatforms') : t('downloadPage.allPlatforms')}
                </p>
                <div className="grid gap-2 sm:grid-cols-2">
                  {others.map((d) => {
                    const target = d.target as Target
                    return (
                      <div key={d.target} className="space-y-1 rounded-md border border-border px-3 py-2">
                        <a
                          href={d.url}
                          download
                          className="flex items-center gap-2 text-sm font-medium hover:text-primary"
                        >
                          <TargetIcon target={target} />
                          {t(TARGET_LABEL_KEYS[target])}
                        </a>
                        <p className="text-xs text-muted-foreground">{t(TARGET_HINT_KEYS[target])}</p>
                      </div>
                    )
                  })}
                </div>
              </div>
            )}

            {data.release_url && (
              <a
                href={data.release_url}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                {t('downloadPage.releaseNotes')}
              </a>
            )}
          </div>
        )}

        <p className="text-xs text-muted-foreground">{t('downloadPage.updateNote')}</p>
      </div>
    </div>
  )
}
