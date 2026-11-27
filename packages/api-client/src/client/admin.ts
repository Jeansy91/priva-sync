import {
  type AdminClient, type AdminJob, type AdminStats, type AdminUser,
  type AuditLogListParams, type AuditLogListResponse,
  type CreateUserRequest,
} from '../types'
import type { HttpClient } from './http'

export function makeAdminApi(http: HttpClient) {
  return {
    listUsers: () =>
      http.request<AdminUser[]>('GET', '/admin/users'),

    createUser: (body: CreateUserRequest) =>
      http.request<AdminUser>('POST', '/admin/users', { body }),

    setQuota: (userID: string, quotaBytes: number) =>
      http.request<void>('PUT', `/admin/users/${userID}/quota`, { body: { quota_bytes: quotaBytes } }),

    setActive: (userID: string, active: boolean) =>
      http.request<void>('PUT', `/admin/users/${userID}/active`, { body: { active } }),

    setRole: (userID: string, role: 'user' | 'admin') =>
      http.request<void>('PUT', `/admin/users/${userID}/role`, { body: { role } }),

    unlockUser: (userID: string) =>
      http.request<void>('POST', `/admin/users/${userID}/unlock`),

    recomputeQuota: (userID: string) =>
      http.request<{ id: string; used_bytes: number; trash_bytes: number; quota_bytes: number }>(
        'POST', `/admin/users/${userID}/recompute-quota`),

    getStats: () =>
      http.request<AdminStats>('GET', '/admin/stats'),

    listAuditLog: (params: AuditLogListParams = {}) => {
      const q = new URLSearchParams()
      if (params.user)   q.set('user', params.user)
      if (params.event)  q.set('event', params.event)
      if (params.from)   q.set('from', params.from)
      if (params.to)     q.set('to', params.to)
      if (params.limit  !== undefined) q.set('limit', String(params.limit))
      if (params.offset !== undefined) q.set('offset', String(params.offset))
      const qs = q.toString()
      return http.request<AuditLogListResponse>(
        'GET', `/admin/audit-log${qs ? `?${qs}` : ''}`)
    },

    listJobs: () =>
      http.request<{ jobs: AdminJob[] }>('GET', '/admin/jobs'),

    triggerJob: (jobID: string) =>
      http.request<void>('POST', `/admin/jobs/${jobID}/run`),

    listClients: () =>
      http.request<{ clients: AdminClient[] }>('GET', '/admin/clients'),

    // Bookkeeping only: no session is revoked, so a device that is still
    // signed in reappears on its next login or token refresh.
    deleteClient: (id: string) =>
      http.request<void>('DELETE', `/admin/clients/${encodeURIComponent(id)}`),
  }
}

export type AdminApi = ReturnType<typeof makeAdminApi>
