import { describe, it, expect, vi } from 'vitest'
import { buildEntryMenuItems } from './entryMenuItems'
import type { DirEntry } from './helpers'
import type { FileEntry } from '@privasync/api-client'

function fakeFile(name: string, path: string): FileEntry {
  return {
    id: 'f-' + name,
    path,
    size: 1,
    mtime: '2026-01-01T00:00:00Z',
    content_hash: '',
    is_deleted: false,
    updated_at: '2026-01-01T00:00:00Z',
  } as FileEntry
}

function fileEntry(name: string): DirEntry {
  return { kind: 'file', name, file: fakeFile(name, '/' + name) }
}
function dirEntry(name: string): DirEntry {
  return { kind: 'dir', name, path: '/' + name }
}

const t = (k: string) => k
const cb = () => ({
  onDelete:   vi.fn(),
  onDownload: vi.fn(),
  onPreview:  vi.fn(),
  onShare:    vi.fn(),
})

describe('buildEntryMenuItems', () => {
  it('liefert Preview+Download+Share+Delete fuer Bild-Datei', () => {
    const c = cb()
    const items = buildEntryMenuItems([fileEntry('photo.jpg')], c, t)
    const labels = items.map((i) => i.label)
    expect(labels).toEqual([
      'files.menuPreviewImage',
      'files.menuDownload',
      'files.menuShare',
      'files.menuDelete',
    ])
  })

  it('liefert Download+Share+Delete fuer Datei ohne Preview', () => {
    const c = cb()
    const items = buildEntryMenuItems([fileEntry('notes.xyz')], c, t)
    expect(items.map((i) => i.label)).toEqual([
      'files.menuDownload',
      'files.menuShare',
      'files.menuDelete',
    ])
  })

  it('liefert ZIP-Download+Share+Delete fuer Ordner', () => {
    const c = cb()
    const items = buildEntryMenuItems([dirEntry('docs')], c, t)
    expect(items.map((i) => i.label)).toEqual([
      'files.menuDownloadZip',
      'files.menuShare',
      'files.menuDelete',
    ])
  })

  it('zeigt Bearbeiten fuer editierbare Textdatei wenn onEditText gesetzt', () => {
    const c = { ...cb(), onEditText: vi.fn() }
    const items = buildEntryMenuItems([fileEntry('config.json')], c, t)
    expect(items.map((i) => i.label)).toContain('files.menuEditText')
    items.find((i) => i.label === 'files.menuEditText')!.onSelect()
    expect(c.onEditText).toHaveBeenCalledWith({ kind: 'file', name: 'config.json', file: expect.any(Object) })
  })

  it('zeigt kein Bearbeiten fuer Nicht-Text', () => {
    const c = { ...cb(), onEditText: vi.fn() }
    const items = buildEntryMenuItems([fileEntry('photo.jpg')], c, t)
    expect(items.map((i) => i.label)).not.toContain('files.menuEditText')
  })

  it('ruft onDownload mit erstem Entry beim Select', () => {
    const c = cb()
    const items = buildEntryMenuItems([fileEntry('a.txt')], c, t)
    items.find((i) => i.label === 'files.menuDownload')!.onSelect()
    expect(c.onDownload).toHaveBeenCalledWith({ kind: 'file', name: 'a.txt', file: expect.any(Object) })
  })

  it('Delete-Item ist destructive', () => {
    const c = cb()
    const items = buildEntryMenuItems([fileEntry('a.txt')], c, t)
    expect(items.find((i) => i.label === 'files.menuDelete')!.variant).toBe('destructive')
  })

  it('Ordner mit allen Callbacks: Oeffnen+Download+NewFolder+Upload+Share+Delete in dieser Reihenfolge', () => {
    const items = buildEntryMenuItems([dirEntry('docs')], {
      ...cb(),
      onOpen:      vi.fn(),
      onNewFolder: vi.fn(),
      onUpload:    vi.fn(),
    }, t)
    expect(items.map((i) => i.label)).toEqual([
      'files.menuOpen',
      'files.menuDownloadZip',
      'files.menuNewFolder',
      'files.menuUpload',
      'files.menuShare',
      'files.menuDelete',
    ])
  })

  it('Datei mit onOpen: kein Open-Eintrag fuer Files (Preview/Abspielen genuegt)', () => {
    const items = buildEntryMenuItems([fileEntry('photo.jpg')], {
      ...cb(),
      onOpen: vi.fn(),
    }, t)
    expect(items.map((i) => i.label)).toEqual([
      'files.menuPreviewImage',
      'files.menuDownload',
      'files.menuShare',
      'files.menuDelete',
    ])
  })

  it('Lokal anzeigen erscheint fuer Datei und Ordner nach Download', () => {
    const c = { ...cb(), onRevealLocal: vi.fn() }
    const fileItems = buildEntryMenuItems([fileEntry('a.txt')], c, t)
    expect(fileItems.map((i) => i.label)).toEqual([
      'files.menuDownload',
      'files.menuRevealLocal',
      'files.menuShare',
      'files.menuDelete',
    ])
    fileItems.find((i) => i.label === 'files.menuRevealLocal')!.onSelect()
    expect(c.onRevealLocal).toHaveBeenCalledWith({ kind: 'file', name: 'a.txt', file: expect.any(Object) })

    const dirItems = buildEntryMenuItems([dirEntry('docs')], c, t)
    expect(dirItems.map((i) => i.label)).toContain('files.menuRevealLocal')
  })

  it('Umbenennen erscheint fuer Dateien zwischen Move und Delete', () => {
    const c = { ...cb(), onMove: vi.fn(), onRename: vi.fn() }
    const items = buildEntryMenuItems([fileEntry('a.txt')], c, t)
    const labels = items.map((i) => i.label)
    expect(labels).toEqual([
      'files.menuDownload',
      'files.menuShare',
      'files.bulkMove',
      'files.menuRename',
      'files.menuDelete',
    ])
    items.find((i) => i.label === 'files.menuRename')!.onSelect()
    expect(c.onRename).toHaveBeenCalledWith({ kind: 'file', name: 'a.txt', file: expect.any(Object) })
  })

  it('Umbenennen erscheint auch fuer Ordner (Server rewrited Subtree)', () => {
    const items = buildEntryMenuItems([dirEntry('docs')], { ...cb(), onRename: vi.fn() }, t)
    expect(items.map((i) => i.label)).toContain('files.menuRename')
  })

  it('Umbenennen erscheint NICHT fuer Mount-Eintraege', () => {
    const mountDir: DirEntry = { kind: 'dir', name: 'shared', path: '/shared', mount: { id: 'm1' } as any }
    const items = buildEntryMenuItems([mountDir], { ...cb(), onRename: vi.fn() }, t)
    expect(items.map((i) => i.label)).not.toContain('files.menuRename')
  })

  it('leere Auswahl mit onNewSyncRoot liefert "Neuer Sync-Ordner"', () => {
    const onNewSyncRoot = vi.fn()
    const items = buildEntryMenuItems([], { ...cb(), onNewSyncRoot }, t)
    expect(items.map((i) => i.label)).toEqual(['files.menuNewSyncRoot'])
    items[0].onSelect()
    expect(onNewSyncRoot).toHaveBeenCalled()
  })

  it('liefert leere Liste bei leerer Auswahl', () => {
    expect(buildEntryMenuItems([], cb(), t)).toEqual([])
  })
})
