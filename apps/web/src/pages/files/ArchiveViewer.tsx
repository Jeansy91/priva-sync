import { useEffect, useState } from 'react'
import { AlertCircle, FileIcon as FileIconLucide, Folder, Loader2 } from 'lucide-react'
import { useTranslation } from '@privasync/i18n'
import { archiveListFrom, type FileSource } from './fileSource'

type Entry = { path: string; size: number; isDir: boolean }

// ArchiveViewer holt die Dateiliste vom Server-Endpoint statt das ZIP komplett
// herunterzuladen — schneller bei großen Archiven, weniger Bandbreite.
export function ArchiveViewer({ source, path }: { source: FileSource; path: string; name: string }) {
  const { t } = useTranslation()
  const [entries, setEntries] = useState<Entry[] | null>(null)
  const [error,   setError]   = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const res = await archiveListFrom(source, path)
        if (cancelled) return
        const items: Entry[] = res.entries.map((e) => ({
          path:  e.name,
          size:  e.size,
          isDir: e.is_dir,
        }))
        items.sort((a, b) => a.path.localeCompare(b.path))
        setEntries(items)
      } catch {
        if (!cancelled) setError(t('files.archiveLoadFailed'))
      }
    })()
    return () => { cancelled = true }
  }, [source, path, t])

  if (error) {
    return (
      <div className="flex items-center justify-center gap-2 py-12 text-sm text-destructive">
        <AlertCircle className="h-4 w-4" />
        <span>{error}</span>
      </div>
    )
  }
  if (!entries) {
    return (
      <div className="flex items-center justify-center gap-2 py-12 text-sm text-muted-foreground">
        <Loader2 className="h-5 w-5 animate-spin" />
        <span>{t('files.archiveLoading')}</span>
      </div>
    )
  }

  return (
    <div className="text-sm">
      <div className="sticky top-0 bg-background border-b border-border px-4 py-2 text-xs text-muted-foreground">
        {entries.length} {t('files.archiveItems')}
      </div>
      <table className="w-full">
        <tbody>
          {entries.map((e) => (
            <tr key={e.path} className="border-b border-border/50">
              <td className="px-4 py-1.5">
                <span className="flex items-center gap-2">
                  {e.isDir
                    ? <Folder className="h-4 w-4 text-blue-500 shrink-0" />
                    : <FileIconLucide className="h-4 w-4 text-muted-foreground shrink-0" />}
                  <span className="truncate">{e.path}</span>
                </span>
              </td>
              <td className="px-4 py-1.5 text-right text-muted-foreground whitespace-nowrap">
                {e.isDir ? '—' : formatBytes(e.size)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}
