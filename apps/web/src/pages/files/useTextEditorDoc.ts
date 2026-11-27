import { useCallback, useEffect, useState } from 'react'
import { loadText, saveText } from './textFileIO'
import { editorLanguage } from './editorMode'
import type { FileSource } from './fileSource'

export type DocStatus = 'loading' | 'ready' | 'saving' | 'error'

// ConflictState carries everything the merge dialog needs: the user's current
// text and the server's competing version, plus the server_version to use as
// If-Match when the user chooses to overwrite.
export interface ConflictState {
  localContent:  string
  serverContent: string
  serverVersion: number | null
}

export interface TextEditorDoc {
  status:    DocStatus
  language:  string
  content:   string
  isDirty:   boolean
  errorKey:  string | null
  conflict:  ConflictState | null
  setContent: (next: string) => void
  save:       () => Promise<void>
  // Conflict resolution: keep the user's edits (overwrite the server) or
  // discard them and adopt the server's current content.
  resolveKeepMine:   () => Promise<void>
  resolveKeepServer: () => void
  dismissConflict:   () => void
}

// useTextEditorDoc owns the load → edit → save lifecycle for one text file,
// including optimistic-lock conflict detection. Monaco stays a dumb view bound
// to `content` / `setContent`; all the behaviour worth testing lives here.
export function useTextEditorDoc(source: FileSource, path: string, name: string): TextEditorDoc {
  const [status,   setStatus]   = useState<DocStatus>('loading')
  const [content,  setContent]  = useState('')
  const [baseline, setBaseline] = useState('')
  const [version,  setVersion]  = useState<number | null>(null)
  const [errorKey, setErrorKey] = useState<string | null>(null)
  const [conflict, setConflict] = useState<ConflictState | null>(null)

  const language = editorLanguage(name) ?? 'plaintext'

  useEffect(() => {
    let cancelled = false
    setStatus('loading')
    setErrorKey(null)
    ;(async () => {
      try {
        const loaded = await loadText(source, path)
        if (cancelled) return
        setContent(loaded.content)
        setBaseline(loaded.content)
        setVersion(loaded.version)
        setStatus('ready')
      } catch {
        if (cancelled) return
        setErrorKey('files.editorLoadFailed')
        setStatus('error')
      }
    })()
    return () => { cancelled = true }
  }, [source, path])

  // commit performs the actual save with a given If-Match version and adopts
  // the result on success. Shared by `save` and the keep-mine resolution.
  const commit = useCallback(async (text: string, ifMatch: number | null): Promise<boolean> => {
    setStatus('saving')
    setErrorKey(null)
    try {
      const res = await saveText(source, path, text, ifMatch !== null ? { ifMatch } : {})
      if (res.status === 'conflict') {
        // Re-fetch the server's current bytes so the merge dialog can diff.
        let serverContent = ''
        try {
          serverContent = (await loadText(source, path)).content
        } catch { /* leave empty; dialog still shows the version clash */ }
        setConflict({ localContent: text, serverContent, serverVersion: res.serverVersion })
        setStatus('ready')
        return false
      }
      setBaseline(text)
      setVersion(res.version)
      setStatus('ready')
      return true
    } catch {
      setErrorKey('files.editorSaveFailed')
      setStatus('error')
      return false
    }
  }, [source, path])

  const save = useCallback(async () => {
    await commit(content, version)
  }, [commit, content, version])

  const resolveKeepMine = useCallback(async () => {
    if (!conflict) return
    const ok = await commit(conflict.localContent, conflict.serverVersion)
    if (ok) setConflict(null)
  }, [commit, conflict])

  const resolveKeepServer = useCallback(() => {
    if (!conflict) return
    setContent(conflict.serverContent)
    setBaseline(conflict.serverContent)
    setVersion(conflict.serverVersion)
    setConflict(null)
    setStatus('ready')
  }, [conflict])

  const dismissConflict = useCallback(() => setConflict(null), [])

  return {
    status, language, content,
    isDirty: content !== baseline,
    errorKey, conflict,
    setContent, save,
    resolveKeepMine, resolveKeepServer, dismissConflict,
  }
}
