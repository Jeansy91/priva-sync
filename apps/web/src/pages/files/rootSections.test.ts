import { describe, it, expect } from 'vitest'
import type { SyncRoot, SharedMount, PathShareInfo } from '@privasync/api-client'
import { buildRootSections, rootSharesByPath } from './rootSections'

function share(over: Partial<PathShareInfo> = {}): PathShareInfo {
  return {
    id:              's-1',
    share_id:        's-1',
    share_type:      'user',
    target_user_id:  'u-bob',
    target_username: 'bob',
    can_write:       false,
    expires_at:      null,
    ...over,
  }
}

function root(over: Partial<SyncRoot> = {}): SyncRoot {
  return {
    id:         'r-' + (over.name ?? 'root'),
    user_id:    'u-self',
    name:       'Photos',
    path:       '/Photos',
    created_at: '2026-05-01T00:00:00Z',
    ...over,
  }
}

function mount(over: Partial<SharedMount> = {}): SharedMount {
  return {
    id:         'm-' + (over.name ?? 'mnt'),
    name:       '2024',
    path:       '/Photos/2024',
    is_file:    false,
    can_write:  true,
    expires_at: null,
    owner:      { id: 'u-alice', username: 'alice', avatar_url: null },
    ...over,
  }
}

describe('buildRootSections', () => {
  it('liefert nur die Eigene-Sektion, wenn keine Mounts da sind', () => {
    const sections = buildRootSections([root({ name: 'Docs' })], [])
    expect(sections).toHaveLength(1)
    expect(sections[0].id).toBe('own')
    expect(sections[0].entries).toHaveLength(1)
    expect(sections[0].entries[0].name).toBe('Docs')
    expect(sections[0].entries[0].kind).toBe('dir')
  })

  it('gruppiert Mounts in eine zweite Sektion mit mount-Decoration', () => {
    const sections = buildRootSections(
      [root({ name: 'Docs' })],
      [mount({ name: '2024' })],
    )
    expect(sections).toHaveLength(2)
    expect(sections[1].id).toBe('shared')
    expect(sections[1].entries[0].kind).toBe('dir')
    expect(sections[1].entries[0].mount?.name).toBe('2024')
  })

  it('sortiert eigene Roots alphabetisch nach Name', () => {
    const sections = buildRootSections(
      [root({ name: 'Videos' }), root({ name: 'Bilder' }), root({ name: 'Docs' })],
      [],
    )
    expect(sections[0].entries.map((e) => e.name)).toEqual(['Bilder', 'Docs', 'Videos'])
  })

  it('Datei-Mounts werden als file-Entry mit mount-Decoration emittiert', () => {
    const fileMount = mount({ name: 'budget.xlsx', is_file: true, path: '/Photos/budget.xlsx' })
    const sections = buildRootSections([], [fileMount])
    const entry = sections[0].entries[0]
    expect(entry.kind).toBe('file')
    expect(entry.mount?.id).toBe(fileMount.id)
    if (entry.kind === 'file') {
      expect(entry.file.path).toBe('/Photos/budget.xlsx')
    }
  })

  it('Datei-Mount ohne Stats trägt leere Platzhalter-Metadaten', () => {
    const fileMount = mount({ id: 'm-f', name: 'cat.jpg', is_file: true, path: '/Photos/cat.jpg' })
    const entry = buildRootSections([], [fileMount])[0].entries[0]
    if (entry.kind !== 'file') throw new Error('expected file entry')
    expect(entry.file.mtime).toBe('')
    expect(entry.file.size).toBe(0)
  })

  it('faltet Lazy-Stats in den Datei-Mount-Eintrag, wenn vorhanden', () => {
    const fileMount = mount({ id: 'm-f', name: 'cat.jpg', is_file: true, path: '/Photos/cat.jpg' })
    const sections = buildRootSections([], [fileMount], {
      'm-f': {
        id:           'real-file-id',
        size:         4096,
        mtime:        '2026-05-20T12:00:00Z',
        updated_at:   '2026-05-21T08:00:00Z',
        content_hash: 'deadbeef',
      },
    })
    const entry = sections[0].entries[0]
    if (entry.kind !== 'file') throw new Error('expected file entry')
    expect(entry.file.id).toBe('real-file-id')
    expect(entry.file.size).toBe(4096)
    expect(entry.file.mtime).toBe('2026-05-20T12:00:00Z')
    expect(entry.file.updated_at).toBe('2026-05-21T08:00:00Z')
    expect(entry.file.content_hash).toBe('deadbeef')
  })

  it('lässt die Shared-Sektion komplett weg, wenn keine Mounts existieren', () => {
    const sections = buildRootSections([root()], [])
    expect(sections.find((s) => s.id === 'shared')).toBeUndefined()
  })

  it('sortiert Mounts alphabetisch nach Name', () => {
    const sections = buildRootSections([], [
      mount({ id: 'm-c', name: 'Charlie' }),
      mount({ id: 'm-a', name: 'Alpha' }),
      mount({ id: 'm-b', name: 'Bravo' }),
    ])
    expect(sections[0].entries.map((e) => e.name)).toEqual(['Alpha', 'Bravo', 'Charlie'])
  })

  it('listet Ordner-Mounts vor Datei-Mounts, je alphabetisch', () => {
    const sections = buildRootSections([], [
      mount({ id: 'm-z', name: 'zebra.txt', is_file: true }),
      mount({ id: 'm-d', name: 'Docs' }),
      mount({ id: 'm-a', name: 'apple.txt', is_file: true }),
      mount({ id: 'm-b', name: 'Bilder' }),
    ])
    expect(sections[0].entries.map((e) => e.name)).toEqual([
      'Bilder', 'Docs', 'apple.txt', 'zebra.txt',
    ])
  })
})

describe('rootSharesByPath', () => {
  it('mapped Root-Share-Listen auf den Root-Pfad', () => {
    const r = root({ id: 'r-1', name: 'Docs' })
    const map = rootSharesByPath([r], { 'r-1': [share({ id: 's-bob' })] })
    expect(map['/Docs']).toHaveLength(1)
    expect(map['/Docs']?.[0].id).toBe('s-bob')
  })

  it('lässt Roots ohne Eintraege ganz aus dem Resultat raus', () => {
    const map = rootSharesByPath([root({ id: 'r-1', name: 'Docs' })], {})
    expect(map).toEqual({})
  })
})
