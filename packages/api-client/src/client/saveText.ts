import { ApiRequestError, type SaveResult } from '../types'

// putText performs an optimistic-lock file save: a raw PUT of the editor's
// bytes with the loaded server_version as If-Match. A 409 is returned as a
// SaveConflict value (the editor opens its merge dialog); other non-2xx
// statuses still throw. Shared by the sync-root and shared-mount transports.
export async function putText(
  url:         string,
  content:     string,
  accessToken: string | null,
  opts:        { ifMatch?: number; mtime?: Date } = {},
): Promise<SaveResult> {
  const headers: Record<string, string> = {
    'Authorization': accessToken ? `Bearer ${accessToken}` : '',
    'Content-Type':  'text/plain; charset=utf-8',
    'X-File-Mtime':  (opts.mtime ?? new Date()).toISOString(),
  }
  if (opts.ifMatch !== undefined) headers['If-Match'] = String(opts.ifMatch)

  const res = await fetch(url, {
    method:      'PUT',
    headers,
    credentials: 'include',
    body:        content,
  })
  return parseSaveResult(res)
}

export async function parseSaveResult(res: Response): Promise<SaveResult> {
  const body = await res.json().catch(() => ({} as Record<string, unknown>))
  if (res.status === 409) {
    return {
      status:        'conflict',
      serverVersion: Number(body.server_version ?? 0),
      serverHash:    String(body.server_hash ?? ''),
      conflictPath:  body.conflict_path ? String(body.conflict_path) : undefined,
    }
  }
  if (!res.ok) throw new ApiRequestError(res.status, body as { error: string })
  return {
    status:      'ok',
    version:     Number(body.server_version ?? 0),
    contentHash: String(body.content_hash ?? ''),
  }
}
