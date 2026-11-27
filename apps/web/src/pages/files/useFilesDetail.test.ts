import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import type { SyncRoot, FileEntry } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { useFilesDetail } from './useFilesDetail'
import type { DetailSelection } from './FileDetailPanel'

const ROOT: SyncRoot = {
  id:         'r-1',
  user_id:    'u-1',
  name:       'Photos',
  path:       'Photos',
  created_at: '2026-01-01T00:00:00Z',
}

beforeEach(() => {
  vi.restoreAllMocks()
  // Directory stats load async and enrich the stub afterwards — not under
  // test here, so keep it pending for the duration of each synchronous test.
  vi.spyOn(apiClient.files, 'directoryStats').mockReturnValue(new Promise(() => {}))
})

function harness(overrides: Partial<Parameters<typeof useFilesDetail>[0]> = {}) {
  const setDetailSelection = vi.fn()
  const { result } = renderHook(() => useFilesDetail({
    selectedRoot:       null,
    currentPath:        '/',
    allFiles:           [] as FileEntry[],
    syncRoots:          [],
    selectedSyncRootID: null,
    detailSelection:    null,
    setDetailSelection,
    ...overrides,
  }))
  return { result, setDetailSelection }
}

describe('useFilesDetail', () => {
  it('liefert nichts wenn weder eine Root offen noch eine Root-Zeile markiert ist', () => {
    const { result } = harness()
    expect(result.current.detailRoot).toBeNull()
    expect(result.current.isListLevel).toBe(false)
    expect(result.current.effectiveSelection).toBeNull()
  })

  it('faellt ohne explizite Auswahl auf das aktuelle Verzeichnis der offenen Root zurueck', () => {
    const { result } = harness({ selectedRoot: ROOT, currentPath: '/docs' })
    expect(result.current.detailRoot).toBe(ROOT)
    expect(result.current.isListLevel).toBe(false)
    expect(result.current.effectiveSelection).toMatchObject({ kind: 'dir', name: 'docs', path: '/docs' })
  })

  it('nutzt den Root-Namen als Ordnernamen wenn der aktuelle Pfad die Root-Wurzel ist', () => {
    const { result } = harness({ selectedRoot: ROOT, currentPath: '/' })
    expect(result.current.effectiveSelection).toMatchObject({ kind: 'dir', name: 'Photos', path: '/' })
  })

  it('gibt eine explizite Datei-Auswahl unveraendert durch', () => {
    const selection: DetailSelection = {
      kind: 'file',
      file: { id: 'f-1', path: '/a.txt', size: 1, mtime: '2026-01-01T00:00:00Z', content_hash: '', is_deleted: false, updated_at: '2026-01-01T00:00:00Z' },
    }
    const { result } = harness({ selectedRoot: ROOT, detailSelection: selection })
    expect(result.current.effectiveSelection).toBe(selection)
  })

  it('Listen-Ebene: eine markierte Root-Zeile wird zum detailRoot und liefert eine Wurzel-Auswahl', () => {
    const { result } = harness({
      selectedRoot: null, syncRoots: [ROOT], selectedSyncRootID: 'r-1',
    })
    expect(result.current.detailRoot).toBe(ROOT)
    expect(result.current.isListLevel).toBe(true)
    expect(result.current.effectiveSelection).toMatchObject({ kind: 'dir', name: 'Photos', path: '/' })
  })

  it('Listen-Ebene ohne markierte Zeile liefert weder detailRoot noch Auswahl', () => {
    const { result } = harness({
      selectedRoot: null, syncRoots: [ROOT], selectedSyncRootID: 'unknown-id',
    })
    expect(result.current.detailRoot).toBeNull()
    expect(result.current.isListLevel).toBe(false)
    expect(result.current.effectiveSelection).toBeNull()
  })
})
