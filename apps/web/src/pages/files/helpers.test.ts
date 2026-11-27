import { describe, it, expect } from 'vitest'
import type { FileEntry, SharedMount } from '@privasync/api-client'
import { formatDate, thumbHostFor, type DirEntry } from './helpers'

function file(over: Partial<FileEntry> = {}): FileEntry {
  return {
    id: 'f-1', path: '/Photos/cat.jpg', size: 10, mtime: '2026-01-01T00:00:00Z',
    content_hash: 'h', is_deleted: false, updated_at: '2026-01-01T00:00:00Z', ...over,
  }
}

function fileMount(over: Partial<SharedMount> = {}): SharedMount {
  return {
    id: 'm-1', name: 'cat.jpg', path: '/Photos/cat.jpg', is_file: true,
    can_write: false, expires_at: null,
    owner: { id: 'u-a', username: 'alice', avatar_url: null }, ...over,
  }
}

describe('formatDate', () => {
  it('rendert ein gültiges ISO-Datum lokalisiert', () => {
    const out = formatDate('2026-04-01T10:30:00Z', 'de')
    expect(out).toMatch(/2026/)
    expect(out).not.toMatch(/Invalid/i)
  })

  it('gibt em-dash statt "Invalid Date" bei leerem String', () => {
    expect(formatDate('', 'de')).toBe('—')
  })

  it('gibt em-dash bei unparsbarem Datum', () => {
    expect(formatDate('not-a-date', 'de')).toBe('—')
  })
})

describe('thumbHostFor', () => {
  it('Datei-Mount: Sub-Pfad ist "/" (Mount-Wurzel ist die Datei)', () => {
    const m = fileMount()
    const entry: DirEntry = { kind: 'file', name: m.name, file: file({ path: m.path }), mount: m }
    expect(thumbHostFor(entry, null)).toEqual({
      rootID: 'mount:m-1', path: '/', mountID: 'm-1',
    })
  })

  it('eigene Datei: Sync-Root-Host mit echtem Pfad', () => {
    const entry: DirEntry = { kind: 'file', name: 'cat.jpg', file: file() }
    expect(thumbHostFor(entry, { id: 'r-1' })).toEqual({
      rootID: 'r-1', path: '/Photos/cat.jpg',
    })
  })

  it('Datei im Mount-Verzeichnis: Mount-Host mit mount-relativem Pfad', () => {
    // Inside a mount the selectedRoot is the share facade (id = share id) and
    // the entry is a plain file (no .mount decoration). The thumb must route
    // through the mount endpoint, not the sync-root one.
    const entry: DirEntry = { kind: 'file', name: 'cat.jpg', file: file({ path: '/Reports/cat.jpg' }) }
    expect(thumbHostFor(entry, { id: 's-1' }, true)).toEqual({
      rootID: 'mount:s-1', path: '/Reports/cat.jpg', mountID: 's-1',
    })
  })

  it('eigene Datei ohne Sync-Root: kein Host', () => {
    const entry: DirEntry = { kind: 'file', name: 'cat.jpg', file: file() }
    expect(thumbHostFor(entry, null)).toBeNull()
  })

  it('Verzeichnis: kein Host', () => {
    const entry: DirEntry = { kind: 'dir', name: 'Photos', path: '/Photos' }
    expect(thumbHostFor(entry, { id: 'r-1' })).toBeNull()
  })
})
