import { describe, it, expect } from 'vitest'
import type { SharedMount } from '@privasync/api-client'
import { nextMountSelection } from './filesPagePrefs'
import type { DetailSelection } from './FileDetailPanel'

function mount(over: Partial<SharedMount> = {}): SharedMount {
  return {
    id:         'm-1',
    name:       '2024',
    path:       '/Photos/2024',
    is_file:    false,
    can_write:  true,
    expires_at: null,
    owner:      { id: 'u-alice', username: 'alice', avatar_url: null },
    ...over,
  }
}

describe('nextMountSelection', () => {
  it('behaelt geladene Stats, wenn derselbe Mount erneut geklickt wird', () => {
    const m = mount()
    const loaded: DetailSelection = {
      kind:        'dir',
      name:        m.name,
      path:        '/' + m.name,
      itemCount:   42,
      totalSize:   1024,
      latestMtime: '2026-05-01T00:00:00Z',
      mount:       m,
    }
    const next = nextMountSelection(loaded, m)
    expect(next.kind).toBe('dir')
    if (next.kind === 'dir') expect(next.itemCount).toBe(42)
  })

  it('liefert frischen Stub, wenn ein anderer Mount geklickt wird', () => {
    const loaded: DetailSelection = {
      kind:        'dir',
      name:        '2024',
      path:        '/2024',
      itemCount:   42,
      totalSize:   1024,
      latestMtime: null,
      mount:       mount({ id: 'm-1' }),
    }
    const next = nextMountSelection(loaded, mount({ id: 'm-2', name: '2025' }))
    expect(next.kind).toBe('dir')
    if (next.kind === 'dir') {
      expect(next.itemCount).toBe(0)
      expect(next.mount?.id).toBe('m-2')
    }
  })

  it('frischt die Mount-Decoration auf, wenn sich der Mount geaendert hat', () => {
    const loaded: DetailSelection = {
      kind:        'dir',
      name:        '2024',
      path:        '/2024',
      itemCount:   42,
      totalSize:   1024,
      latestMtime: null,
      mount:       mount({ id: 'm-1', can_write: false }),
    }
    const next = nextMountSelection(loaded, mount({ id: 'm-1', can_write: true }))
    expect(next.mount?.can_write).toBe(true)
    if (next.kind === 'dir') expect(next.itemCount).toBe(42)
  })
})
