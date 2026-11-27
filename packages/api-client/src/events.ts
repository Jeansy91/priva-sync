// Typed SSE event bindings. Wraps the tygo-generated structs in events.gen.ts
// into an ergonomic EventMap so consumers can write
//   useSSE({ file_uploaded: typed(({ file }) => ...) })
// without re-stating the envelope shape at every call site.

import type {
  ConflictAdded,
  Envelope,
  FileAdded,
  FileDeleted,
  FileMoved,
  FileRestored,
  FileUploaded,
  QuotaThresholdCrossed,
  QuotaUpdated,
  TrashAdded,
  TrashEmptied,
} from './events.gen'

export * from './events.gen'

// SSEEventMap is the source of truth for typed events. Add new entries when
// migrating an untyped event to the Go-First codegen path.
export interface SSEEventMap {
  file_uploaded: FileUploaded
  file_added: FileAdded
  file_moved: FileMoved
  file_deleted: FileDeleted
  file_restored: FileRestored
  conflict_added: ConflictAdded
  quota_updated: QuotaUpdated
  quota_threshold_crossed: QuotaThresholdCrossed
  trash_added: TrashAdded
  trash_emptied: TrashEmptied
}

export type SSEEventName = keyof SSEEventMap

export type SSEEventEnvelope<K extends SSEEventName> = Envelope<SSEEventMap[K]> & {
  type: K
}

// typed unwraps the envelope and passes the payload to `fn`. It returns the
// shape useSSE expects (handler taking `unknown`), but the inner function is
// fully typed — no `any` casts at the call site.
export function typed<K extends SSEEventName>(
  fn: (data: SSEEventMap[K], envelope: SSEEventEnvelope<K>) => void,
): (raw: unknown) => void {
  return (raw) => {
    if (typeof raw !== 'object' || raw === null) return
    const env = raw as SSEEventEnvelope<K>
    if (!env.data) return
    fn(env.data, env)
  }
}
