// URL parsing for the Files-Browser. Two shapes:
//   /files/<rootName>/<sub/path>       — own sync-root browsing
//   /files/_m/<shareID>/<sub/path>     — Shared Mount browsing (ADR-0010)
//
// `_m` is a reserved first-segment marker — sync-root names cannot start
// with an underscore in the UI, so there is no collision.

export type ParsedFilesUrl = {
  mountMode:    boolean
  mountIDParam: string | null
  mountSubPath: string
  urlRootName:  string | null
  urlPath:      string
}

export function parseFilesUrl(splat: string): ParsedFilesUrl {
  const segments = splat.split('/').filter(Boolean)
  const mountMode = segments[0] === '_m'

  if (mountMode) {
    const mountIDParam = segments[1] ? decodeURIComponent(segments[1]) : null
    const sub = segments.length > 2
      ? '/' + segments.slice(2).map(decodeURIComponent).join('/')
      : '/'
    return {
      mountMode:    true,
      mountIDParam,
      mountSubPath: sub,
      urlRootName:  null,
      urlPath:      sub,
    }
  }
  return {
    mountMode:    false,
    mountIDParam: null,
    mountSubPath: '/',
    urlRootName:  segments[0] ? decodeURIComponent(segments[0]) : null,
    urlPath:      segments.length > 1
      ? '/' + segments.slice(1).map(decodeURIComponent).join('/')
      : '/',
  }
}
