import type { SaveResult } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { downloadFrom, type FileSource } from './fileSource'

// LoadedText is the editor's starting state for a file: its text plus the
// optimistic-lock baseline (server_version) used as If-Match on save. `version`
// is null when the server omits the X-Server-Version header (older build) — the
// editor then saves without If-Match and falls back to real-conflict handling.
export interface LoadedText {
  content: string
  version: number | null
  hash:    string | null
}

// loadText fetches a file's bytes as UTF-8 text together with the version
// headers, from either a sync-root or a Shared Mount.
export async function loadText(src: FileSource, path: string): Promise<LoadedText> {
  const res = await downloadFrom(src, path)
  if (!res.ok) throw new Error(`download failed: ${res.status}`)
  const content = await res.text()
  const v = res.headers.get('X-Server-Version')
  return {
    content,
    version: v !== null && v !== '' ? Number(v) : null,
    hash:    res.headers.get('X-Content-Hash'),
  }
}

// saveText writes the edited content back through the matching transport,
// forwarding the optimistic-lock version as If-Match. Returns a SaveResult so
// the caller can branch on the 'conflict' case.
export function saveText(
  src:     FileSource,
  path:    string,
  content: string,
  opts:    { ifMatch?: number } = {},
): Promise<SaveResult> {
  return src.kind === 'mount'
    ? apiClient.sharedMounts.saveText(src.mountID, path, content, opts)
    : apiClient.files.saveText(src.rootID, path, content, opts)
}
