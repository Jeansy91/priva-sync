import type { FileEntry } from '@privasync/api-client'

// Server-side search lives in the API (see ADR-0008). This module only
// exports the hit type the UI uses to render the response. The former
// in-memory `searchEntries` function was removed because it did not
// scale to million-file roots.
export type SearchHit =
  | { kind: 'file'; name: string; file: FileEntry; relativeDir: string }
  | { kind: 'dir';  name: string; path: string;    relativeDir: string }
