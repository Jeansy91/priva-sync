import { ApiRequestError, type ApiError } from '../types'

/**
 * Outcome of a token refresh. The three cases are not interchangeable:
 * only `unauthorized` means the session is over. A transport error or a 5xx
 * is `unavailable` — the caller sees the original failure, but stays logged
 * in and refreshes again on the next 401 (ADR-0046).
 */
export type RefreshResult =
  | { status: 'refreshed'; accessToken: string }
  | { status: 'unauthorized' }
  | { status: 'unavailable' }

export interface RequestOptions {
  body?:    unknown
  query?:   Record<string, string>
  headers?: Record<string, string>
  signal?:  AbortSignal
}

/**
 * Thin HTTP layer used by every domain factory. Owns the access token,
 * the unauthorized + refresh callbacks, and the `request` JSON helper.
 * Domain APIs that bypass JSON (Blob upload/download) reach for the
 * `baseURL` + `accessToken` getters directly.
 */
export class HttpClient {
  readonly baseURL: string
  private _accessToken: string | null = null
  private onUnauthorized?: () => void
  private refreshFn?: () => Promise<RefreshResult>
  private pendingRefresh: Promise<RefreshResult> | null = null

  constructor(baseURL: string) {
    this.baseURL = baseURL.replace(/\/$/, '')
  }

  get accessToken(): string | null {
    return this._accessToken
  }

  setAccessToken(token: string | null) {
    this._accessToken = token
  }

  /**
   * Called when a request gets a 401 that could not be recovered by refreshing
   * the token.  Use this to trigger a logout and redirect to login.
   */
  setOnUnauthorized(cb: () => void) {
    this.onUnauthorized = cb
  }

  /**
   * Optional token-refresh function. Called on 401 before giving up. It must
   * distinguish a rejected session from a temporarily unreachable server —
   * see RefreshResult.
   */
  setRefreshFn(fn: () => Promise<RefreshResult>) {
    this.refreshFn = fn
  }

  /** Resolves the absolute base, falling back to window.location.origin
   *  for the Vite dev proxy where `baseURL` is empty. */
  absoluteBase(): string {
    return this.baseURL || (typeof window !== 'undefined' ? window.location.origin : '')
  }

  /** Authorization header for raw fetch() calls outside the JSON pipeline. */
  authHeaders(): Record<string, string> {
    return this._accessToken ? { 'Authorization': `Bearer ${this._accessToken}` } : {}
  }

  async request<T>(
    method: string,
    path: string,
    options: RequestOptions = {},
    isRetry = false,
  ): Promise<T> {
    const url = new URL(`${this.absoluteBase()}/api/v1${path}`)
    if (options.query) {
      for (const [k, v] of Object.entries(options.query)) {
        if (v !== undefined) url.searchParams.set(k, v)
      }
    }

    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...options.headers,
    }

    const sentWith = this._accessToken
    if (sentWith) {
      headers['Authorization'] = `Bearer ${sentWith}`
    }

    const res = await fetch(url.toString(), {
      method,
      headers,
      credentials: 'include',  // for the refresh-token cookie
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
      signal: options.signal,
    })

    if (!res.ok) {
      // On 401: try a token refresh once, then give up and notify the caller.
      // Skip recovery for /auth/refresh itself — otherwise a failing refresh
      // would invoke refreshFn() recursively and burn the call stack.
      if (res.status === 401 && !isRetry && path !== '/auth/refresh') {
        if (await this.recoverFrom401(sentWith)) {
          return this.request<T>(method, path, options, true)
        }
      }

      let errorBody: ApiError = { error: res.statusText }
      try {
        errorBody = await res.json()
      } catch { /* keep statusText */ }
      throw new ApiRequestError(res.status, errorBody)
    }

    if (res.status === 204 || res.status === 202) return undefined as T
    const text = await res.text()
    if (!text) return undefined as T
    return JSON.parse(text)
  }

  /** Reports whether the request may be retried. Ends the session only when
   *  the refresh came back as a definitive `unauthorized`. */
  private async recoverFrom401(sentWith: string | null): Promise<boolean> {
    // Someone else already refreshed while this request was in flight: the
    // 401 answers a token that is no longer current, so retrying is enough.
    if (this._accessToken !== sentWith) return true

    if (!this.refreshFn) {
      this.onUnauthorized?.()
      return false
    }

    const outcome = await this.refreshOnce(this.refreshFn)
    if (outcome.status === 'refreshed') return true
    if (outcome.status === 'unauthorized') this.onUnauthorized?.()
    return false
  }

  /** Single-flight refresh. Rotation retires the refresh token, so N parallel
   *  401s must share one refresh — otherwise every loser of that race presents
   *  a retired token and, once its grace window is up, ends the session
   *  (ADR-0046). */
  private refreshOnce(refreshFn: () => Promise<RefreshResult>): Promise<RefreshResult> {
    this.pendingRefresh ??= refreshFn()
      .then((result) => {
        if (result.status === 'refreshed') this.setAccessToken(result.accessToken)
        return result
      })
      .catch((): RefreshResult => ({ status: 'unavailable' }))
      .finally(() => { this.pendingRefresh = null })
    return this.pendingRefresh
  }
}
