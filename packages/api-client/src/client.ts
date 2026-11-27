import type { PathShareInfo } from './types'
import { HttpClient, type RefreshResult } from './client/http'
import { makeAuthApi,         type AuthApi }         from './client/auth'
import { makeFilesApi,        type FilesApi }        from './client/files'
import { makeSharedMountsApi, type SharedMountsApi } from './client/sharedMounts'
import { makeSharesApi,       type SharesApi }       from './client/shares'
import { makeUsersApi,        type UsersApi }        from './client/users'
import { makeTrashApi,        type TrashApi }        from './client/trash'
import { makeSetupApi,        type SetupApi }        from './client/setup'
import { makeAdminApi,        type AdminApi }        from './client/admin'

export class ApiClient {
  private http: HttpClient
  auth:         AuthApi
  files:        FilesApi
  sharedMounts: SharedMountsApi
  shares:       SharesApi
  users:        UsersApi
  trash:        TrashApi
  setup:        SetupApi
  admin:        AdminApi

  constructor(baseURL: string) {
    this.http         = new HttpClient(baseURL)
    this.auth         = makeAuthApi(this.http)
    this.files        = makeFilesApi(this.http)
    this.sharedMounts = makeSharedMountsApi(this.http)
    this.shares       = makeSharesApi(this.http)
    this.users        = makeUsersApi(this.http)
    this.trash        = makeTrashApi(this.http)
    this.setup        = makeSetupApi(this.http)
    this.admin        = makeAdminApi(this.http)
  }

  setAccessToken(token: string | null) { this.http.setAccessToken(token) }

  /**
   * Called when a request gets a 401 that could not be recovered by refreshing
   * the token.  Use this to trigger a logout and redirect to login.
   */
  setOnUnauthorized(cb: () => void) { this.http.setOnUnauthorized(cb) }

  /**
   * Optional token-refresh function. Called on 401 before giving up. It must
   * distinguish a rejected session from a temporarily unreachable server —
   * see RefreshResult.
   */
  setRefreshFn(fn: () => Promise<RefreshResult>) { this.http.setRefreshFn(fn) }

  // Top-level helper that doesn't fit any domain bucket cleanly.
  sharesByRoot = (rootID: string) =>
    this.http.request<Record<string, PathShareInfo[]>>('GET', `/sync-roots/${rootID}/shares`)

  /**
   * Opens a Server-Sent Events stream. Returns an EventSource.
   * The caller is responsible for closing it (eventSource.close()).
   */
  openEventStream(): EventSource {
    // EventSource cannot set request headers, so the stream authenticates via
    // the scoped, HttpOnly sse_token cookie the server sets on login and every
    // refresh. withCredentials is what makes the browser send it. Never put
    // the access token in the URL: it would land in the server's request log,
    // any reverse-proxy log and the browser history (ADR-0038).
    return new EventSource(
      `${this.http.baseURL}/api/v1/events`,
      { withCredentials: true }
    )
  }
}
