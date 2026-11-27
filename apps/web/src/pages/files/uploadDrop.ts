import type { UploadItem } from './UploadSplitButton'
import { toUploadItems } from './UploadSplitButton'

// FileSystemEntry-Walk (drag/drop von Ordnern). Browser exposed nur per
// webkitGetAsEntry()/createReader() — readEntries muss bis [] iterieren,
// weil Chrome max ~100 pro Call liefert.
function readAllEntries(reader: any): Promise<any[]> {
  return new Promise((resolve, reject) => {
    const out: any[] = []
    const pump = () => {
      reader.readEntries((batch: any[]) => {
        if (batch.length === 0) resolve(out)
        else { out.push(...batch); pump() }
      }, reject)
    }
    pump()
  })
}

async function walkEntry(entry: any): Promise<UploadItem[]> {
  if (entry.isFile) {
    const file: File = await new Promise((res, rej) => entry.file(res, rej))
    // fullPath starts with '/'. We want it relative, without the leading slash.
    const rel = (entry.fullPath as string).replace(/^\/+/, '') || file.name
    return [{ file, relPath: rel }]
  }
  if (entry.isDirectory) {
    const children = await readAllEntries(entry.createReader())
    const nested = await Promise.all(children.map(walkEntry))
    return nested.flat()
  }
  return []
}

/**
 * Build UploadItem[] from a drop event. Prefers DataTransferItemList +
 * webkitGetAsEntry so directories are walked recursively; falls back to
 * the flat `files` list when entries are unavailable.
 */
export async function uploadItemsFromDrop(dt: DataTransfer): Promise<UploadItem[]> {
  const items = dt.items
  if (items && items.length > 0 && typeof (items[0] as any).webkitGetAsEntry === 'function') {
    const entries: any[] = []
    for (let i = 0; i < items.length; i++) {
      const ent = (items[i] as any).webkitGetAsEntry?.()
      if (ent) entries.push(ent)
    }
    if (entries.length > 0) {
      const lists = await Promise.all(entries.map(walkEntry))
      return lists.flat()
    }
  }
  return dt.files?.length ? toUploadItems(dt.files) : []
}
