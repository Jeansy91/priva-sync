import { describe, it, expect } from 'vitest'
import { uploadItemsFromDrop } from './uploadDrop'

// Minimaler FileSystemEntry-Mock — Browser-API ist nur per Webkit-Schnittstelle
// verfügbar und in jsdom nicht abgebildet.
function fileEntry(name: string, fullPath: string, file: File) {
  return {
    isFile: true, isDirectory: false, name, fullPath,
    file: (cb: (f: File) => void) => cb(file),
  }
}

function dirEntry(name: string, fullPath: string, children: unknown[]) {
  return {
    isFile: false, isDirectory: true, name, fullPath,
    createReader: () => {
      let done = false
      return {
        readEntries: (cb: (es: unknown[]) => void) => {
          if (done) return cb([])
          done = true
          cb(children)
        },
      }
    },
  }
}

function dropWith(entries: unknown[]): DataTransfer {
  return {
    files: [] as unknown as FileList,
    items: entries.map((e) => ({
      kind: 'file',
      webkitGetAsEntry: () => e,
    })) as unknown as DataTransferItemList,
  } as DataTransfer
}

describe('uploadItemsFromDrop', () => {
  it('läuft Verzeichnisse rekursiv ab und nimmt den fullPath ohne führenden Slash', async () => {
    const f = new File(['x'], 'bar.txt')
    const dir = dirEntry('dir', '/dir', [fileEntry('bar.txt', '/dir/bar.txt', f)])

    const items = await uploadItemsFromDrop(dropWith([dir]))

    expect(items).toHaveLength(1)
    expect(items[0].relPath).toBe('dir/bar.txt')
    expect(items[0].file).toBe(f)
  })

  it('verarbeitet flache Datei-Drops über file.name', async () => {
    const f = new File(['x'], 'foo.txt')
    const items = await uploadItemsFromDrop(dropWith([fileEntry('foo.txt', '/foo.txt', f)]))

    expect(items).toHaveLength(1)
    expect(items[0].relPath).toBe('foo.txt')
  })

  it('verarbeitet verschachtelte Verzeichnisse', async () => {
    const a = new File(['a'], 'a.txt')
    const b = new File(['b'], 'b.txt')
    const inner = dirEntry('sub', '/top/sub', [fileEntry('b.txt', '/top/sub/b.txt', b)])
    const top   = dirEntry('top', '/top', [
      fileEntry('a.txt', '/top/a.txt', a),
      inner,
    ])

    const items = await uploadItemsFromDrop(dropWith([top]))

    expect(items.map((i) => i.relPath).sort()).toEqual(['top/a.txt', 'top/sub/b.txt'])
  })

  it('fällt auf dt.files zurück, wenn keine Entries verfügbar sind', async () => {
    const f = new File(['x'], 'plain.txt')
    const dt = {
      files: [f] as unknown as FileList,
      items: [] as unknown as DataTransferItemList,
    } as DataTransfer

    const items = await uploadItemsFromDrop(dt)

    expect(items).toHaveLength(1)
    expect(items[0].relPath).toBe('plain.txt')
  })
})
