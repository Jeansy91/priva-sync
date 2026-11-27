import type {
  CreateShareRequest, PublicShareView, Share, ShareAccess, UpdateGrantRequest,
} from '../types'
import type { HttpClient } from './http'

export function makeSharesApi(http: HttpClient) {
  return {
    list: () =>
      http.request<Share[]>('GET', '/shares'),

    // get-or-create the share aggregate for a path and, for user-shares, upsert
    // the recipient's grant. Returns the share with grant_id / dual tokens.
    create: (rootID: string, body: CreateShareRequest) =>
      http.request<Share>('POST', `/sync-roots/${rootID}/shares`, { body }),

    // Owner-only in-place change of one recipient's grant (can_write, expiry).
    // Avoids the revoke+recreate dance that would tear down the recipient's
    // mount; the server broadcasts share_updated. See ADR-0015.
    updateGrant: (shareID: string, userID: string, body: UpdateGrantRequest) =>
      http.request<Share>('PATCH', `/shares/${shareID}/grants/${userID}`, { body }),

    // Owner removes one recipient from a share aggregate.
    removeGrant: (shareID: string, userID: string) =>
      http.request<void>('DELETE', `/shares/${shareID}/grants/${userID}`),

    // Effective access map for a path inside one of the caller's own roots:
    // recipients split into writers/readers, inherited from ancestor shares.
    access: (rootID: string, path: string) =>
      http.request<ShareAccess>('GET', `/sync-roots/${rootID}/access`, { query: { path } }),

    // Generate one public link (read|write) for a path — independent lifecycle.
    // The optional expiry is part of the link: chosen at creation, gone when
    // the link is regenerated or deleted.
    createPublicLink: (rootID: string, path: string, kind: 'read' | 'write', expiresAt: string | null = null) =>
      http.request<Share>('POST', `/sync-roots/${rootID}/public-links`, { body: { path, kind, expires_at: expiresAt } }),

    deletePublicLink: (rootID: string, path: string, kind: 'read' | 'write') =>
      http.request<void>('DELETE', `/sync-roots/${rootID}/public-links`, { query: { path, kind } }),

    delete: (shareID: string) =>
      http.request<void>('DELETE', `/shares/${shareID}`),

    getPublic: (token: string) =>
      http.request<Share>('GET', `/public/shares/${token}`),

    publicEntries: (token: string, sub?: string) =>
      http.request<PublicShareView>(
        'GET',
        `/public/shares/${token}/entries`,
        sub ? { query: { sub } } : {},
      ),

    publicDownloadURL: (token: string, sub?: string): string => {
      const u = new URL(`${http.absoluteBase()}/api/v1/public/shares/${token}/download`)
      if (sub) u.searchParams.set('sub', sub)
      return u.toString()
    },
  }
}

export type SharesApi = ReturnType<typeof makeSharesApi>
