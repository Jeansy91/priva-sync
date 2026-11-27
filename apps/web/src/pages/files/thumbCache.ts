// Modul-globaler Cache für Tabellen-Thumbnails (RowThumb).
// Verhindert, dass beim Re-Mount (Listen-Refresh, SSE-Event, Scroll) die
// Datei erneut geladen wird — RowThumb liest den vorhandenen Blob-URL aus
// dem Cache, statt jedes Mal neu zu fetchen.

const cache = new Map<string, string>()

export function thumbCacheKey(rootID: string, path: string, size: number): string {
  return `${rootID}::${path}::${size}`
}

export function getCachedThumb(key: string): string | null {
  return cache.get(key) ?? null
}

export function setCachedThumb(key: string, url: string) {
  // Falls dieser Slot vorher schon einen anderen URL hielt: alte URL freigeben.
  const prev = cache.get(key)
  if (prev && prev !== url) {
    try { URL.revokeObjectURL(prev) } catch {}
  }
  cache.set(key, url)
}

export function invalidateThumb(rootID: string, path: string) {
  for (const key of cache.keys()) {
    if (key.startsWith(`${rootID}::${path}::`)) {
      const url = cache.get(key)
      if (url) { try { URL.revokeObjectURL(url) } catch {} }
      cache.delete(key)
    }
  }
}
