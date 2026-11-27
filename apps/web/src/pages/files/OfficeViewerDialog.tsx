import { useEffect, useId, useRef, useState } from 'react'
import { AlertCircle, Loader2 } from 'lucide-react'
import { Dialog, DialogContent, DialogClose } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { officeConfigFrom, type FileSource } from './fileSource'

// Minimal shape of the OnlyOffice-API-compatible document server's global,
// loaded at runtime from the externally configured `document_server_url`
// (ADR-0027) — never bundled, since the URL is only known at deploy time.
interface DocEditorInstance {
  destroyEditor: () => void
}
declare global {
  interface Window {
    DocsAPI?: {
      DocEditor: new (containerId: string, config: unknown) => DocEditorInstance
    }
  }
}

// In-flight/settled load promises keyed by script src. Needed because a
// second open re-querying the DOM for an existing <script> tag would attach
// listeners for 'load'/'error' events that already fired once and never
// fire again — the promise would then never settle (dead spinner forever).
// Caching the promise itself, not the DOM node, sidesteps that: every
// caller of loadDocsApi for the same src gets the same settled/settling
// promise. Failed attempts are evicted so the next open retries with a
// fresh <script> tag instead of being stuck on a dead one.
const loadPromises = new Map<string, Promise<void>>()

// Loads the document server's DocsAPI script once per URL.
function loadDocsApi(documentServerURL: string): Promise<void> {
  const src = `${documentServerURL.replace(/\/$/, '')}/web-apps/apps/api/documents/api.js`
  if (window.DocsAPI) return Promise.resolve()
  const cached = loadPromises.get(src)
  if (cached) return cached
  const promise = new Promise<void>((resolve, reject) => {
    const script = document.createElement('script')
    script.src = src
    script.async = true
    script.onload = () => {
      // A 200 response that isn't actually the DocsAPI script (reverse
      // proxy serving an SPA index/error page for the path, misconfigured
      // document_server.url, ...) fires 'load' without ever defining the
      // global. Treat that the same as a network error rather than
      // silently leaving the caller waiting forever.
      if (window.DocsAPI) resolve()
      else reject(new Error('docsapi-global-missing'))
    }
    script.onerror = () => reject(new Error('script'))
    document.head.appendChild(script)
  })
  promise.catch(() => loadPromises.delete(src))
  loadPromises.set(src, promise)
  return promise
}

// Office preview/edit (OOXML, ODF, legacy binary — ADR-0049) rendered by an
// externally configured, OnlyOffice-API-compatible document server
// (ADR-0027, edit + save-back per ADR-0033). Fetches a signed, short-lived
// editor config — view-only or editable with a callback URL wired up,
// decided server-side from the requester's write access and whether the
// format round-trips — then hands it to the document server's own
// DocsAPI.DocEditor, which manages its own iframe (and, in edit mode, its
// own save-back calls) internally. Any failure (server not configured,
// unreachable, unsupported type) falls back to the same "not supported"
// message as other unsupported formats — never an unexplained error state.
export function OfficeViewerDialog({
  source, path, name, onClose,
}: {
  source:  FileSource
  path:    string
  name:    string
  onClose: () => void
}) {
  const { t } = useTranslation()
  const containerId = useId().replace(/:/g, '-')
  const containerRef = useRef<HTMLDivElement>(null)
  const editorRef = useRef<DocEditorInstance | null>(null)
  const [ready, setReady] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const cfg = await officeConfigFrom(source, path)
        // loadDocsApi only ever resolves once window.DocsAPI is actually
        // set (see there) — it never resolves with the global missing, so
        // reaching here without it would be a logic error, not a case to
        // silently swallow.
        await loadDocsApi(cfg.document_server_url)
        if (cancelled) return
        if (!window.DocsAPI) throw new Error('docsapi-global-missing')
        editorRef.current = new window.DocsAPI.DocEditor(containerId, cfg.config)
        if (!cancelled) setReady(true)
      } catch {
        if (!cancelled) setError(t('files.previewUnsupported'))
      }
    })()
    return () => {
      cancelled = true
      editorRef.current?.destroyEditor()
      editorRef.current = null
    }
  }, [source, path, t, containerId])

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="max-w-none max-h-none w-[99vw] h-[99vh] mx-0 p-0 flex flex-col">
        <div className="flex items-center justify-between px-4 py-2 border-b border-border shrink-0">
          <div className="truncate text-sm font-medium" title={name}>{name}</div>
          <DialogClose onClose={onClose} className="static" />
        </div>
        <div className="flex-1 min-h-0 relative">
          {error ? (
            <div className="flex items-center justify-center gap-2 py-12 text-sm text-muted-foreground">
              <AlertCircle className="h-4 w-4" />
              <span>{error}</span>
            </div>
          ) : (
            <>
              {!ready && (
                <div className="absolute inset-0 flex items-center justify-center">
                  <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                </div>
              )}
              <div id={containerId} ref={containerRef} className="w-full h-full" />
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
