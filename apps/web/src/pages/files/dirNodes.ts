import type { FileEntry } from '@privasync/api-client'

// Liefert die direkten Unterordner-Namen unter parentPath aus einer flachen
// FileEntry-Liste (Backend liefert rekursiv pro sync-root). Sortierung ist
// case-insensitive, damit der Tree wie ein Datei-Manager wirkt.
export function directSubdirs(files: FileEntry[], parentPath: string): string[] {
  const prefix = parentPath.endsWith('/') ? parentPath : parentPath + '/'
  const seen = new Set<string>()
  for (const f of files) {
    if (!f.path.startsWith(prefix)) continue
    const rel = f.path.slice(prefix.length)
    if (!rel) continue
    const slash = rel.indexOf('/')
    if (slash === -1) {
      // Direct child entry: only count as a subfolder when the row is an
      // explicit directory entry (is_dir=true).
      if (f.is_dir) seen.add(rel)
      continue
    }
    seen.add(rel.slice(0, slash))
  }
  return [...seen].sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }))
}
