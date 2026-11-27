import { describe, it, expect } from 'vitest'
import type { SyncRoot } from '@privasync/api-client'
import { buildTrashTree } from './TrashPage'

const root: SyncRoot = {
  id: 'r1', user_id: 'u', name: 'Photos', path: '/Photos',
  created_at: '2026-01-01T00:00:00Z',
}

function entry(id: string, path: string) {
  return {
    id, path, size: 1, content_hash: '', deleted_at: '2026-01-01T00:00:00Z',
    rootID: 'r1', rootName: 'Photos',
  }
}

describe('buildTrashTree', () => {
  it('liefert keinen top-knoten fuer leere entries', () => {
    expect(buildTrashTree([root], [])).toEqual([])
  })

  it('liefert nur roots mit mind. einem entry', () => {
    const tree = buildTrashTree([root], [entry('f1', '/x.txt')])
    expect(tree).toHaveLength(1)
    expect(tree[0].kind).toBe('root')
    expect((tree[0] as any).name).toBe('Photos')
  })

  it('legt verschachtelte Ordner und Dateien an', () => {
    const tree = buildTrashTree([root], [
      entry('f1', '/A/x.txt'),
      entry('f2', '/A/B/y.txt'),
    ])
    const a = (tree[0] as any).children.find((c: any) => c.kind === 'folder' && c.name === 'A')
    expect(a.kind).toBe('folder')
    const b = (a as any).children.find((c: any) => c.kind === 'folder' && c.name === 'B')
    expect(b).toBeDefined()
    const x = (a as any).children.find((c: any) => c.kind === 'file' && c.id === 'f1')
    expect(x).toBeDefined()
    const y = (b as any).children.find((c: any) => c.kind === 'file' && c.id === 'f2')
    expect(y).toBeDefined()
  })

  it('sortiert kinder alphabetisch, ordner vor dateien', () => {
    const tree = buildTrashTree([root], [
      entry('f1', '/zeta.txt'),
      entry('f2', '/Alpha/inner.txt'),
      entry('f3', '/alpha.txt'),
      entry('f4', '/Bravo/inner.txt'),
    ])
    const children = (tree[0] as any).children as any[]
    const names = children.map((c) => c.name ?? c.entry.path.split('/').pop())
    // Expect folders Alpha, Bravo (case-insens.), then files alpha.txt, zeta.txt
    expect(children[0].kind).toBe('folder')
    expect(children[1].kind).toBe('folder')
    expect(children[2].kind).toBe('file')
    expect(children[3].kind).toBe('file')
    expect(names).toEqual(['Alpha', 'Bravo', 'alpha.txt', 'zeta.txt'])
  })

  it('sortiert verschachtelte ordnerinhalte ebenfalls', () => {
    const tree = buildTrashTree([root], [
      entry('f1', '/A/zeta.txt'),
      entry('f2', '/A/alpha.txt'),
      entry('f3', '/A/Sub/inner.txt'),
    ])
    const a = (tree[0] as any).children.find((c: any) => c.name === 'A')
    const kinds = a.children.map((c: any) => c.kind)
    expect(kinds).toEqual(['folder', 'file', 'file'])
    const names = a.children.map((c: any) => c.name ?? c.entry.path.split('/').pop())
    expect(names).toEqual(['Sub', 'alpha.txt', 'zeta.txt'])
  })

  it('blendet roots ohne entries aus', () => {
    const root2: SyncRoot = { ...root, id: 'r2', name: 'Docs' }
    const tree = buildTrashTree([root, root2], [entry('f1', '/x.txt')])
    expect(tree).toHaveLength(1)
    expect((tree[0] as any).name).toBe('Photos')
  })

  it('verteilt Entries auf den richtigen Root', () => {
    const root2: SyncRoot = { ...root, id: 'r2', name: 'Docs' }
    const tree = buildTrashTree([root, root2], [
      entry('f1', '/x.txt'),
      { ...entry('f2', '/y.txt'), rootID: 'r2', rootName: 'Docs' },
    ])
    expect((tree[0] as any).fileIds).toEqual(['f1'])
    expect((tree[1] as any).fileIds).toEqual(['f2'])
  })
})
