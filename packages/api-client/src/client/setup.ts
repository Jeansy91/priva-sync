import type { HttpClient } from './http'

export function makeSetupApi(http: HttpClient) {
  return {
    status: () =>
      http.request<{ needs_setup: boolean }>('GET', '/setup/status'),

    complete: (body: { token: string; username: string; email: string; password: string }) =>
      http.request<{ id: string; username: string; email: string }>('POST', '/setup/', { body }),
  }
}

export type SetupApi = ReturnType<typeof makeSetupApi>
