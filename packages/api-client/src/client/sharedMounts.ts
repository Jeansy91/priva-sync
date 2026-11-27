import {
  ApiRequestError,
  type ConflictVersions,
  type OfficeConfigResponse,
  type ResolveStrategy,
  type SaveResult,
  type SharedMount,
  type UserSummaryWithAvatar,
} from '../types'
import type { HttpClient } from './http'
import { putText } from './saveText'

export function makeSharedMountsApi(http: HttpClient) {
  return {
    list: () =>
      http.request<SharedMount[]>('GET', '/shared-mounts'),

    files: (shareID: string, path: string) =>
      http.request<{
        entries: Array<{
          name:          string
          path:          string
          is_dir:        boolean
          size:          number
          mtime?:        string
          content_hash?: string
        }>
      }>(
        'GET',
        `/shared-mounts/${shareID}/files`,
        { query: { path } },
      ),

    // Thumbnail for a file inside a Shared Mount. Mirrors files.thumbnail
    // but goes through the mount-scoped endpoint so the recipient does
    // not need direct access to the giver's sync root. See ADR-0010.
    thumbnail: (shareID: string, path: string, size?: number, fit?: 'square' | 'contain'): Promise<Response> => {
      // absoluteBase(), not baseURL: with the Vite dev proxy baseURL is empty
      // and `new URL('/api/v1/…')` would throw "Invalid URL". See http.ts.
      const u = new URL(`${http.absoluteBase()}/api/v1/shared-mounts/${shareID}/thumbnail`)
      u.searchParams.set('path', path)
      if (size) u.searchParams.set('size', size.toString())
      if (fit)  u.searchParams.set('fit',  fit)
      return fetch(u.toString(), {
        headers:     http.authHeaders(),
        credentials: 'include',
      })
    },

    download: (shareID: string, path: string, opts: { range?: string } = {}): Promise<Response> => {
      const u = new URL(`${http.absoluteBase()}/api/v1/shared-mounts/${shareID}/download`)
      u.searchParams.set('path', path)
      return fetch(u.toString(), {
        headers: {
          ...http.authHeaders(),
          ...(opts.range ? { Range: opts.range } : {}),
        },
        credentials: 'include',
      })
    },

    // Media-stream fetch token (ADR-0067), mount-scoped counterpart of
    // files.mediaStreamUrl. Read access to the mount is enough — playback
    // never needs can_write.
    mediaStreamUrl: async (shareID: string, path: string): Promise<{ url: string; expiresIn: number }> => {
      const { token, expires_in } = await http.request<{ token: string; expires_in: number }>(
        'POST', `/shared-mounts/${shareID}/files/media-token`, { query: { path } },
      )
      return { url: `${http.baseURL}/media/${token}`, expiresIn: expires_in }
    },

    // Archive (.zip) listing for a file inside a Shared Mount. Mirrors
    // files.archiveList but goes through the mount-scoped endpoint. For a
    // file-mount `path` is "/". See ADR-0012.
    archiveList: (shareID: string, path: string) =>
      http.request<{ entries: { name: string; size: number; is_dir: boolean }[] }>(
        'GET', `/shared-mounts/${shareID}/archive`, { query: { path } },
      ),

    // Signed, short-lived editor config for read-only Office preview inside
    // a Shared Mount. Mirrors files.officeConfig but goes through the
    // mount-scoped endpoint. See ADR-0027. 404 means the document server is
    // not configured.
    officeConfig: (shareID: string, path: string) =>
      http.request<OfficeConfigResponse>(
        'GET', `/shared-mounts/${shareID}/files/office-config`, { query: { path } },
      ),

    delete: (shareID: string, path: string) =>
      http.request<void>('DELETE', `/shared-mounts/${shareID}/files`, { query: { path } }),

    // Recipient-side "leave the mount" action. Removes the user-share
    // row this mount is bound to; the server emits share_revoked to
    // both giver and recipient. See ADR-0010.
    leave: (shareID: string) =>
      http.request<void>('DELETE', `/shared-mounts/${shareID}`),

    // Lazy stats for the detail panel. Server discriminates via `kind`:
    // file → meta of the underlying file; dir → recursive aggregate.
    // See ADR-0012.
    stats: (shareID: string) =>
      http.request<
        | {
            kind:            'file'
            id:              string
            size:            number
            mtime:           string
            content_hash:    string
            updated_at:      string
            last_modifier?:  UserSummaryWithAvatar | null
          }
        | {
            kind:        'dir'
            item_count:  number
            file_count:  number
            total_size:  number
            latest_mtime?: string | null
          }
      >('GET', `/shared-mounts/${shareID}/stats`),

    // Native browser download: mint a short-lived token, then point the
    // browser at /download/{token}. Mirrors files.syncRootDownloadUrl —
    // without `path` the mount root, a directory comes back as ZIP, a file
    // as itself. See ADR-0012, ADR-0073.
    downloadUrl: async (shareID: string, path?: string): Promise<string> => {
      const grant = await http.request<{ token: string }>(
        'POST', `/shared-mounts/${shareID}/download-token`,
        path && path !== '/' ? { query: { path } } : undefined,
      )
      return `${http.baseURL}/download/${grant.token}`
    },

    // Open conflicts inside the mount (ADR-0011). Paths are relativized to
    // the mount root by the server; never the giver's absolute layout.
    conflicts: (shareID: string) =>
      http.request<Array<{
        id:            string
        file_id:       string
        conflict_path: string
        created_at:    string
      }>>('GET', `/shared-mounts/${shareID}/conflicts`),

    // Both versions' metadata for the resolve dialog, paths mount-relative.
    conflictVersions: (shareID: string, conflictID: string) =>
      http.request<ConflictVersions>('GET', `/shared-mounts/${shareID}/conflicts/${conflictID}`),

    // The loser copy's bytes, served from the giver's storage.
    conflictFile: (shareID: string, conflictID: string): Promise<Response> =>
      fetch(`${http.absoluteBase()}/api/v1/shared-mounts/${shareID}/conflicts/${conflictID}/file`, {
        headers:     http.authHeaders(),
        credentials: 'include',
      }),

    // Resolve a conflict inside the mount. Requires can_write; the server
    // resolves under the giver so quota/storage stay with the owner.
    resolveConflict: (shareID: string, conflictID: string, strategy?: ResolveStrategy) =>
      http.request<void>('POST', `/shared-mounts/${shareID}/conflicts/${conflictID}/resolve`,
        strategy ? { body: { strategy } } : undefined),

    move: (shareID: string, from: string, to: string) =>
      http.request<{ id: string; path: string }>('PATCH', `/shared-mounts/${shareID}/files`, {
        body: { op: 'move', from, to },
      }),

    upload: (shareID: string, path: string, file: Blob, mtime?: Date) =>
      fetch(`${http.baseURL}/api/v1/shared-mounts/${shareID}/files?path=${encodeURIComponent(path)}`, {
        method: 'POST',
        headers: {
          'Authorization': http.accessToken ? `Bearer ${http.accessToken}` : '',
          'X-File-Mtime':  mtime?.toISOString() ?? new Date().toISOString(),
        },
        credentials: 'include',
        body: file,
      }).then(async (res) => {
        if (!res.ok) throw new ApiRequestError(res.status, await res.json())
        return res.json()
      }),

    // Optimistic-lock text save into a writable Shared Mount. Mirrors
    // files.saveText; `ifMatch` is the loaded server_version.
    saveText: (shareID: string, path: string, content: string, opts: { ifMatch?: number; mtime?: Date } = {}): Promise<SaveResult> =>
      putText(
        `${http.absoluteBase()}/api/v1/shared-mounts/${shareID}/files?path=${encodeURIComponent(path)}`,
        content, http.accessToken, opts,
      ),
  }
}

export type SharedMountsApi = ReturnType<typeof makeSharedMountsApi>
