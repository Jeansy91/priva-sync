import { describe, it, expect } from 'vitest'
import type { FileEntry } from '@privasync/api-client'
import { directSubdirs } from './dirNodes'

function f(path: string): FileEntry {
  return {
    id: path, path, size: 0, mtime: '', content_hash: '',
    is_deleted: false, updated_at: '',
  }
}

describe('directSubdirs', () => {
  it('liefert leere liste fuer leere eingabe', () => {
    expect(directSubdirs([], '/')).toEqual([])
  })

  it('liefert direkte unterordner unter root', () => {
    const files = [f('/a/x.txt'), f('/b/y.txt'), f('/a/sub/z.txt')]
    expect(directSubdirs(files, '/')).toEqual(['a', 'b'])
  })

  it('ignoriert dateien direkt im parent', () => {
    const files = [f('/top.txt'), f('/a/x.txt')]
    expect(directSubdirs(files, '/')).toEqual(['a'])
  })

  it('listet nur unterordner unterhalb von parentPath', () => {
    const files = [
      f('/a/x.txt'),
      f('/a/sub1/x.txt'),
      f('/a/sub2/x.txt'),
      f('/b/y.txt'),
    ]
    expect(directSubdirs(files, '/a')).toEqual(['sub1', 'sub2'])
  })

  it('dedupliziert bei mehreren dateien im selben unterordner', () => {
    const files = [f('/a/sub/x.txt'), f('/a/sub/y.txt'), f('/a/sub/z.txt')]
    expect(directSubdirs(files, '/a')).toEqual(['sub'])
  })

  it('sortiert alphabetisch case-insensitive', () => {
    const files = [f('/zeta/x'), f('/Alpha/x'), f('/beta/x')]
    expect(directSubdirs(files, '/')).toEqual(['Alpha', 'beta', 'zeta'])
  })

  it('zeigt is_dir-eintraege ohne nested-children als unterordner', () => {
    const empty: FileEntry = { ...f('/leer'), is_dir: true }
    expect(directSubdirs([empty], '/')).toEqual(['leer'])
  })

  it('mergt is_dir und content-basierte unterordner ohne duplikate', () => {
    const files: FileEntry[] = [
      f('/a/x.txt'),
      { ...f('/a'), is_dir: true },
      { ...f('/b'), is_dir: true },
    ]
    expect(directSubdirs(files, '/')).toEqual(['a', 'b'])
  })
})
