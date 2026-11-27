import type { UserSummary } from '../types'
import type { HttpClient } from './http'

export function makeUsersApi(http: HttpClient) {
  return {
    search: (q: string, signal?: AbortSignal) =>
      http.request<UserSummary[]>('GET', '/users/search', { query: { q }, signal }),
  }
}

export type UsersApi = ReturnType<typeof makeUsersApi>
