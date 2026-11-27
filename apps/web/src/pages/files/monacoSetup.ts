import * as monaco from 'monaco-editor'
import editorWorker from 'monaco-editor/editor/editor.worker.js?worker'
import jsonWorker from 'monaco-editor/language/json/json.worker.js?worker'
import cssWorker from 'monaco-editor/language/css/css.worker.js?worker'
import htmlWorker from 'monaco-editor/language/html/html.worker.js?worker'
import tsWorker from 'monaco-editor/language/typescript/ts.worker.js?worker'
import { loader } from '@monaco-editor/react'

// Self-host Monaco: bind the loader to the bundled package and wire the
// language workers through Vite's ?worker imports. This keeps everything
// served from our own origin — no CDN fetch, which a privacy product must not
// do. setupMonaco is idempotent; the dialog calls it before first render.
let configured = false

export function setupMonaco(): void {
  if (configured) return
  configured = true

  ;(self as unknown as { MonacoEnvironment: monaco.Environment }).MonacoEnvironment = {
    getWorker(_workerId: string, label: string) {
      switch (label) {
        case 'json':                          return new jsonWorker()
        case 'css': case 'scss': case 'less':  return new cssWorker()
        case 'html': case 'handlebars': case 'razor': return new htmlWorker()
        case 'typescript': case 'javascript':  return new tsWorker()
        default:                               return new editorWorker()
      }
    },
  }

  loader.config({ monaco })
}
