import {
  ApiRequestError,
  type Conflict,
  type ConflictVersions,
  type FileEntry,
  type OfficeConfigResponse,
  type ResolveStrategy,
  type SaveResult,
  type UploadResponse,
} from '../types'
import type { HttpClient } from './http'
import { putText } from './saveText'

export function makeFilesApi(http: HttpClient) {
  return {
    listSyncRoots: () =>
      http.request<import('../types').SyncRoot[]>('GET', '/sync-roots'),

    createSyncRoot: (name: string) =>
      http.request<import('../types').SyncRoot>('POST', '/sync-roots', { body: { name } }),

    renameSyncRoot: (rootID: string, name: string) =>
      http.request<import('../types').SyncRoot>('PATCH', `/sync-roots/${rootID}`, { body: { name } }),

    deleteSyncRoot: (rootID: string) =>
      http.request<void>('DELETE', `/sync-roots/${rootID}`),

    downloadSyncRoot: (rootID: string) =>
      fetch(`${http.baseURL}/api/v1/sync-roots/${rootID}/download`, {
        headers:     http.authHeaders(),
        credentials: 'include',
      }),

    /** Mint a short-lived URL the browser can hit without an Authorization
     *  header — needed for native streaming downloads (otherwise
     *  fetch().blob() buffers the whole body in RAM before the save dialog
     *  even appears). Without `path` the URL serves the whole root as ZIP; a
     *  directory `path` scopes the ZIP to that subtree, a file `path` serves
     *  the file itself (ADR-0073). */
    syncRootDownloadUrl: async (rootID: string, path?: string): Promise<string> => {
      const body = path && path !== '/' ? { path } : undefined
      const { token } = await http.request<{ token: string; expires_in: number }>(
        'POST', `/sync-roots/${rootID}/download-token`, body ? { body } : undefined,
      )
      return `${http.baseURL}/download/${token}`
    },

    /** Lists the direct children at `path` (one level). Pagination via cursor.
     *  Implicit directories (derived from file paths only) come back as
     *  synthesized FileEntry rows with empty id/mtime. See ADR-0008. */
    list: async (
      rootID: string,
      path = '/',
      opts: { limit?: number; cursor?: string } = {},
    ): Promise<{ files: FileEntry[]; next_cursor: string }> => {
      // Every entry is a row on the server (ADR-0041), so id, mtime and
      // updated_at always arrive. content_hash stays optional: a directory
      // has none.
      type ListEntry = {
        name:          string
        path:          string
        is_dir:        boolean
        id:            string
        size:          number
        mtime:         string
        content_hash?: string
        updated_at:    string
      }
      const res = await http.request<{ entries: ListEntry[]; next_cursor: string }>(
        'GET', `/sync-roots/${rootID}/files`,
        {
          query: {
            path,
            ...(opts.limit  !== undefined ? { limit:  String(opts.limit) } : {}),
            ...(opts.cursor !== undefined ? { cursor: opts.cursor }        : {}),
          },
        },
      )
      const files: FileEntry[] = (res.entries ?? []).map((e) => ({
        id:           e.id,
        path:         e.path,
        size:         e.size,
        mtime:        e.mtime,
        content_hash: e.content_hash ?? '',
        is_deleted:   false,
        updated_at:   e.updated_at,
        is_dir:       e.is_dir,
      }))
      return { files, next_cursor: res.next_cursor ?? '' }
    },

    /** Recursive directory statistics for the detail panel. */
    directoryStats: (rootID: string, path: string) =>
      http.request<{
        item_count:   number
        file_count:   number
        total_size:   number
        latest_mtime?: string
      }>('GET', `/sync-roots/${rootID}/directories/stats`, { query: { path } }),

    /** Server-side search inside a sync-root. Recursive under `path`,
     *  excludes trashed entries. Wildcard `*` is supported, other SQL
     *  metacharacters are treated literally. See ADR-0008. */
    search: (
      rootID: string,
      q: string,
      opts: { path?: string; limit?: number; cursor?: string } = {},
    ) =>
      http.request<{ files: FileEntry[]; next_cursor: string }>(
        'GET', `/sync-roots/${rootID}/files/search`,
        {
          query: {
            q,
            ...(opts.path   !== undefined ? { path:   opts.path }            : {}),
            ...(opts.limit  !== undefined ? { limit:  String(opts.limit) }   : {}),
            ...(opts.cursor !== undefined ? { cursor: opts.cursor }          : {}),
          },
        },
      ),

    /** Global search across every sync-root the caller owns. Returns one
     *  group per root with hits. Empty groups are omitted. Per-group
     *  load-more reuses the per-root `search` endpoint. */
    searchGlobal: (
      q: string,
      opts: { limit?: number } = {},
    ) =>
      http.request<{
        groups: {
          sync_root_id:   string
          sync_root_name: string
          files:          FileEntry[]
          next_cursor:    string
        }[]
      }>(
        'GET', `/files/search`,
        {
          query: {
            q,
            ...(opts.limit !== undefined ? { limit: String(opts.limit) } : {}),
          },
        },
      ),

    upload: (rootID: string, path: string, file: Blob, mtime?: Date) =>
      fetch(`${http.baseURL}/api/v1/sync-roots/${rootID}/files?path=${encodeURIComponent(path)}`, {
        method: 'PUT',
        headers: {
          'Authorization': http.accessToken ? `Bearer ${http.accessToken}` : '',
          'X-File-Mtime': mtime?.toISOString() ?? new Date().toISOString(),
        },
        credentials: 'include',
        body: file,
      }).then(async (res) => {
        if (!res.ok) throw new ApiRequestError(res.status, await res.json())
        return res.json() as Promise<UploadResponse>
      }),

    download: (rootID: string, path: string, opts: { range?: string } = {}) =>
      fetch(`${http.baseURL}/api/v1/sync-roots/${rootID}/files/download?path=${encodeURIComponent(path)}`, {
        headers: {
          ...http.authHeaders(),
          ...(opts.range ? { Range: opts.range } : {}),
        },
        credentials: 'include',
      }),

    /** Mint a short-lived, multi-use URL a native <video>/<audio> element can
     *  use directly as `src` — the browser cannot send our Authorization
     *  header on a native src, so playback is gated by this opaque grant
     *  instead. Unlike syncRootDownloadUrl's token, this one is not consumed
     *  on first use: a player fires many separate Range requests while
     *  seeking/buffering. `expiresIn` (seconds) lets the caller renew the
     *  grant proactively before it lapses mid-playback. See ADR-0067. */
    mediaStreamUrl: async (rootID: string, path: string): Promise<{ url: string; expiresIn: number }> => {
      const { token, expires_in } = await http.request<{ token: string; expires_in: number }>(
        'POST', `/sync-roots/${rootID}/files/media-token`, { query: { path } },
      )
      return { url: `${http.baseURL}/media/${token}`, expiresIn: expires_in }
    },

    // Optimistic-lock text save for the web editor. `ifMatch` is the
    // server_version loaded from the download's X-Server-Version header; a
    // stale value comes back as a SaveConflict (see putText).
    saveText: (rootID: string, path: string, content: string, opts: { ifMatch?: number; mtime?: Date } = {}): Promise<SaveResult> =>
      putText(
        `${http.absoluteBase()}/api/v1/sync-roots/${rootID}/files?path=${encodeURIComponent(path)}`,
        content, http.accessToken, opts,
      ),

    thumbnail: (
      rootID: string,
      path:   string,
      size  = 256,
      fit: 'square' | 'contain' = 'square',
    ) =>
      fetch(
        `${http.baseURL}/api/v1/sync-roots/${rootID}/files/thumbnail`
        + `?path=${encodeURIComponent(path)}&size=${size}&fit=${fit}`,
        {
          headers:     http.authHeaders(),
          credentials: 'include',
        },
      ),

    archiveList: (rootID: string, path: string) =>
      http.request<{ entries: { name: string; size: number; is_dir: boolean }[] }>(
        'GET', `/sync-roots/${rootID}/files/archive`, { query: { path } },
      ),

    // Signed, short-lived editor config for Office preview/edit against
    // the externally configured document server (ADR-0027, formats per
    // ADR-0049). 404 means the document server is not configured.
    officeConfig: (rootID: string, path: string) =>
      http.request<OfficeConfigResponse>(
        'GET', `/sync-roots/${rootID}/files/office-config`, { query: { path } },
      ),

    delete: (rootID: string, path: string) =>
      http.request<void>('DELETE', `/sync-roots/${rootID}/files`, { query: { path } }),

    move: (rootID: string, from: string, to: string) =>
      http.request<FileEntry>('PATCH', `/sync-roots/${rootID}/files`, {
        body: { op: 'move', from, to },
      }),

    // Rename is a move that keeps the parent directory and only swaps the
    // last path segment. Server-side it is the same op=move; this wrapper
    // exists so callsites read as intent ("rename") rather than "move".
    rename: (rootID: string, from: string, to: string) =>
      http.request<FileEntry>('PATCH', `/sync-roots/${rootID}/files`, {
        body: { op: 'move', from, to },
      }),

    // Copy duplicates a file at `to`. The server gates quota (507 when over)
    // and answers 409 when `to` is already occupied. Folder copies use the
    // same op=copy — the server resolves file-vs-directory from the source
    // row; copyFolder is a readability alias for folder callsites.
    copy: (rootID: string, from: string, to: string) =>
      http.request<FileEntry>('PATCH', `/sync-roots/${rootID}/files`, {
        body: { op: 'copy', from, to },
      }),

    copyFolder: (rootID: string, from: string, to: string) =>
      http.request<FileEntry>('PATCH', `/sync-roots/${rootID}/files`, {
        body: { op: 'copy', from, to },
      }),

    createDirectory: (rootID: string, path: string, mtime?: Date) =>
      http.request<FileEntry>(
        'POST', `/sync-roots/${rootID}/directories`,
        { body: { path, mtime: (mtime ?? new Date()).toISOString() } },
      ),

    /** Keyset-paginated changes-feed (ADR-0036). `since` is the seq cursor of
     *  the last row already seen (omit for a first sync), not a timestamp --
     *  page through by re-calling with the returned `next_cursor` until it's
     *  absent. */
    getChanges: (rootID: string, opts: { since?: number; limit?: number } = {}) =>
      http.request<{ changes: FileEntry[]; next_cursor?: number }>(
        'GET', `/sync-roots/${rootID}/changes`,
        {
          query: {
            ...(opts.since !== undefined ? { since: String(opts.since) } : {}),
            ...(opts.limit !== undefined ? { limit: String(opts.limit) } : {}),
          },
        },
      ),

    listConflicts: (rootID: string) =>
      http.request<Conflict[]>('GET', `/sync-roots/${rootID}/conflicts`),

    // Both versions' metadata for the resolve dialog.
    getConflict: (conflictID: string) =>
      http.request<ConflictVersions>('GET', `/conflicts/${conflictID}`),

    // The loser copy's bytes. Upload/move conflicts have no files row at the
    // conflict path, so the path-based download cannot serve them.
    conflictFile: (conflictID: string): Promise<Response> =>
      fetch(`${http.absoluteBase()}/api/v1/conflicts/${conflictID}/file`, {
        headers:     http.authHeaders(),
        credentials: 'include',
      }),

    resolveConflict: (conflictID: string, strategy?: ResolveStrategy) =>
      http.request<void>('POST', `/conflicts/${conflictID}/resolve`,
        strategy ? { body: { strategy } } : undefined),
  }
}

export type FilesApi = ReturnType<typeof makeFilesApi>
