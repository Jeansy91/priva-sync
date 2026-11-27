import type { FileEntry, TrashEntry } from '../types'
import type { HttpClient } from './http'

export function makeTrashApi(http: HttpClient) {
  return {
    list: (rootID: string) =>
      http.request<TrashEntry[]>('GET', `/sync-roots/${rootID}/trash`),

    restore: (rootID: string, fileID: string) =>
      http.request<FileEntry>('POST', `/sync-roots/${rootID}/trash/${fileID}/restore`),

    purge: (rootID: string, fileID: string) =>
      http.request<void>('DELETE', `/sync-roots/${rootID}/trash/${fileID}`),

    empty: () =>
      http.request<void>('DELETE', `/trash`),
  }
}

export type TrashApi = ReturnType<typeof makeTrashApi>
