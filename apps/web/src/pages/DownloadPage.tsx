import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Apple, ArrowLeft, Download, Loader2, Monitor, Terminal } from 'lucide-react'
import { Button } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { useAuthStore } from '../store/auth'

type Target = 'windows-x86_64' | 'darwin-x86_64' | 'darwin-aarch64' | 'linux-x86_64' | 'linux-aarch64'

interface PlatformArtifact {
  signature: string
  url:       string
}

interface Manifest {
  version:   string
  pub_date?: string
  platforms: Record<string, PlatformArtifact>
}

function detectTarget(): Target {
  const ua   = navigator.userAgent
  const plat = navigator.platform || ''

  if (/Win/i.test(plat) || /Windows/i.test(ua))     return 'windows-x86_64'
  if (/Mac/i.test(plat)) {
    // arm64 Macs advertise as Intel in userAgent; use userAgentData when available.
    const arch = (navigator as any).userAgentData?.getHighEntropyValues?.(['architecture'])
    if (arch?.architecture === 'arm')               return 'darwin-aarch64'
    if (/ARM|Apple Silicon/i.test(ua))              return 'darwin-aarch64'
    return 'darwin-x86_64'
  }
  if (/aarch64|arm64/i.test(ua))                    return 'linux-aarch64'
  return 'linux-x86_64'
}

const TARGET_LABELS: Record<Target, string> = {
  'windows-x86_64':  'Windows · x86_64',
  'darwin-x86_64':   'macOS · Intel',
  'darwin-aarch64':  'macOS · Apple Silicon',
  'linux-x86_64':    'Linux · x86_64',
  'linux-aarch64':   'Linux · aarch64',
}

function TargetIcon({ target }: { target: Target }) {
  if (target.startsWith('windows')) return <Monitor   className="h-4 w-4" />
  if (target.startsWith('darwin'))  return <Apple     className="h-4 w-4" />
  return <Terminal className="h-4 w-4" />
}

const RELEASES_TAG_URL = 'https://github.com/Jeansy91/priva-sync/releases/tag'

export default function DownloadPage() {
  const { t }                       = useTranslation()
  const isAuthenticated             = useAuthStore((s) => s.isAuthenticated)
  const [detected, setDetected]     = useState<Target>(() => detectTarget())
  const [manifest, setManifest]     = useState<Manifest | null>(null)
  const [loading, setLoading]       = useState(true)
  const [error, setError]           = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    ;(async () => {
      setLoading(true)
      setError(null)
      try {
        const res = await fetch(`/updates/${detected}/0.0.0`, { signal: ctrl.signal })
        if (res.status === 204) {
          setManifest(null)
          return
        }
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        setManifest(await res.json() as Manifest)
      } catch (err) {
        if ((err as Error).name === 'AbortError') return
        setError('Konnte Release-Informationen nicht laden.')
      } finally {
        setLoading(false)
      }
    })()
    return () => ctrl.abort()
  }, [detected])

  const primary = manifest?.platforms[detected]
  const others  = manifest
    ? (Object.keys(manifest.platforms) as Target[]).filter((t) => t !== detected)
    : []

  return (
    <div className="min-h-screen bg-background p-4">
      <div className="mx-auto max-w-2xl py-12 space-y-8">
        <Link
          to={isAuthenticated ? '/files' : '/login'}
          className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-3.5 w-3.5" /> {isAuthenticated ? 'Zurück' : 'Zurück zum Login'}
        </Link>

        <div className="space-y-2">
          <h1 className="text-3xl font-semibold tracking-tight">PrivaSync Desktop</h1>
          <p className="text-muted-foreground">
            Lade den Client für dein Betriebssystem herunter. Updates installieren sich später automatisch.
          </p>
        </div>

        {loading && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" />
            Lade Release-Informationen…
          </div>
        )}

        {error && (
          <div className="rounded-md border border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive">
            {error}
          </div>
        )}

        {!loading && !error && !manifest && (
          <div className="rounded-md border border-border bg-muted/30 px-4 py-8 text-center text-sm text-muted-foreground">
            Es steht noch kein Release bereit.
          </div>
        )}

        {manifest && primary && (
          <div className="space-y-4">
            <div className="rounded-lg border border-border bg-card p-6 space-y-4">
              <div className="flex items-center justify-between gap-3">
                <div>
                  <p className="text-xs uppercase tracking-wide text-muted-foreground">Empfohlen für dich</p>
                  <p className="mt-1 flex items-center gap-2 text-base font-medium">
                    <TargetIcon target={detected} />
                    {TARGET_LABELS[detected]}
                  </p>
                </div>
                <Button asChild size="lg">
                  <a href={primary.url} download>
                    <Download className="h-4 w-4" />
                    Version {manifest.version} herunterladen
                  </a>
                </Button>
              </div>
              <a
                href={`${RELEASES_TAG_URL}/v${manifest.version}`}
                target="_blank"
                rel="noopener noreferrer"
                className="block border-t border-border pt-4 text-sm text-muted-foreground hover:text-foreground"
              >
                {t('downloadPage.releaseNotes')}
              </a>
            </div>

            {manifest && primary == null && (
              <div className="rounded-md border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
                Für {TARGET_LABELS[detected]} ist noch kein Build vorhanden. Wähle unten eine andere Plattform.
              </div>
            )}

            {others.length > 0 && (
              <div className="rounded-lg border border-border bg-card p-4 space-y-2">
                <p className="text-xs uppercase tracking-wide text-muted-foreground">Andere Plattformen</p>
                <div className="grid gap-1.5 sm:grid-cols-2">
                  {others.map((t) => (
                    <a
                      key={t}
                      href={manifest.platforms[t].url}
                      download
                      className="flex items-center gap-2 rounded-md border border-border
                                 px-3 py-2 text-sm hover:bg-accent/30"
                      onClick={() => setDetected(t)}
                    >
                      <TargetIcon target={t} />
                      {TARGET_LABELS[t]}
                    </a>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}

        <p className="text-xs text-muted-foreground">
          Nach der Installation signiert der eingebaute Updater jede Aktualisierung mit Ed25519.
          Bei Erstinstallation zeigt Windows ggf. SmartScreen, macOS den Gatekeeper — beides ist erwartet.
        </p>
      </div>
    </div>
  )
}
