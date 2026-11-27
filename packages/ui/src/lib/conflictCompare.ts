// Pure helpers behind the conflict version comparison (ADR-0003). Kept free of
// React so web and desktop share one set of rules and they are unit-testable.

/** Image types every webview renders natively; TIFF and RAW are left out. */
const PREVIEWABLE_IMAGE_EXTENSIONS = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'avif']

/** Largest image either side may be and still get an inline preview. */
export const PREVIEW_SIZE_LIMIT = 25 * 1024 * 1024

export function isPreviewableImage(path: string): boolean {
  const dot = path.lastIndexOf('.')
  if (dot < 0) return false
  return PREVIEWABLE_IMAGE_EXTENSIONS.includes(path.slice(dot + 1).toLowerCase())
}

/** Exact byte count with locale grouping, e.g. "1.234.567 Bytes". */
export function formatExactBytes(bytes: number, locale: string): string {
  return new Intl.NumberFormat(locale, {
    style: 'unit',
    unit: 'byte',
    unitDisplay: 'long',
    maximumFractionDigits: 0,
  }).format(bytes)
}

/** Rounded size for orientation next to the exact value, e.g. "1.2 MB". */
export function formatHumanBytes(bytes: number, locale: string): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = bytes
  let i = -1
  do {
    value /= 1024
    i++
  } while (value >= 1024 && i < units.length - 1)
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(value)} ${units[i]}`
}

/** Date and time down to the second. Empty string for an invalid date. */
export function formatTimestamp(value: string | Date, locale: string): string {
  const d = typeof value === 'string' ? new Date(value) : value
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString(locale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

export interface VersionFacts {
  sizeBytes: number | null
  modifiedAt: string | Date | null
}

export interface VersionDifferences {
  size: boolean
  modified: boolean
  /** Size and modification time match: very likely the same content. */
  identical: boolean
}

// File systems and the database keep time at different resolutions (NTFS
// 100 ns, Postgres 1 µs, FAT 2 s), so equality is judged to the second.
function toSeconds(v: string | Date): number {
  const ms = (typeof v === 'string' ? new Date(v) : v).getTime()
  return Math.floor(ms / 1000)
}

/**
 * Which facts differ between two versions. A fact that is unknown on either
 * side — or when `compareDates` is false because one date is only a stand-in —
 * is neither "different" nor counted towards "identical".
 */
export function compareVersions(a: VersionFacts, b: VersionFacts, compareDates = true): VersionDifferences {
  const sizeKnown = a.sizeBytes !== null && b.sizeBytes !== null
  const modifiedKnown = compareDates && a.modifiedAt !== null && b.modifiedAt !== null
  const size = sizeKnown && a.sizeBytes !== b.sizeBytes
  const modified = modifiedKnown && toSeconds(a.modifiedAt as string | Date) !== toSeconds(b.modifiedAt as string | Date)
  return {
    size,
    modified,
    identical: sizeKnown && modifiedKnown && !size && !modified,
  }
}
