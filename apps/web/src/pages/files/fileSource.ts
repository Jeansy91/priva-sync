import { apiClient } from '../../store/auth'

// FileSource abstracts where a preview/viewer fetches its bytes from: a
// sync-root the caller owns, or a Shared Mount. Every viewer takes a
// FileSource instead of a raw rootID so file-mounts behave exactly like
// directory files. See ADR-0010 / ADR-0012.
export type FileSource =
  | { kind: 'root';  rootID:  string }
  | { kind: 'mount'; mountID: string }

export function downloadFrom(src: FileSource, path: string): Promise<Response> {
  return src.kind === 'mount'
    ? apiClient.sharedMounts.download(src.mountID, path)
    : apiClient.files.download(src.rootID, path)
}

// Range-scoped download — used where only a small window of the file is
// needed (e.g. the first 1 MB for ID3 cover-art extraction) so the caller
// doesn't pull the whole file just to read a few bytes of it. The server
// answers Range (ADR-0047); a proxy that ignores it still returns 200 with
// the full body, which the caller must tolerate.
export function downloadRangeFrom(src: FileSource, path: string, range: string): Promise<Response> {
  return src.kind === 'mount'
    ? apiClient.sharedMounts.download(src.mountID, path, { range })
    : apiClient.files.download(src.rootID, path, { range })
}

// Mints a short-lived, multi-use URL a native <video>/<audio> element can
// use directly as `src` — see ADR-0067.
export function mediaStreamUrlFrom(src: FileSource, path: string): Promise<{ url: string; expiresIn: number }> {
  return src.kind === 'mount'
    ? apiClient.sharedMounts.mediaStreamUrl(src.mountID, path)
    : apiClient.files.mediaStreamUrl(src.rootID, path)
}

export function thumbnailFrom(
  src:  FileSource,
  path: string,
  size?: number,
  fit?:  'square' | 'contain',
): Promise<Response> {
  return src.kind === 'mount'
    ? apiClient.sharedMounts.thumbnail(src.mountID, path, size, fit)
    : apiClient.files.thumbnail(src.rootID, path, size, fit)
}

export function archiveListFrom(src: FileSource, path: string) {
  return src.kind === 'mount'
    ? apiClient.sharedMounts.archiveList(src.mountID, path)
    : apiClient.files.archiveList(src.rootID, path)
}

// Signed, short-lived editor config for Office preview/edit. See ADR-0027
// for the wiring and ADR-0049 for the supported formats.
export function officeConfigFrom(src: FileSource, path: string) {
  return src.kind === 'mount'
    ? apiClient.sharedMounts.officeConfig(src.mountID, path)
    : apiClient.files.officeConfig(src.rootID, path)
}
