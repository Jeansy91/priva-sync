import { describe, it, expect, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import type { SyncRoot } from '@privasync/api-client'
import type { UploadSplitButtonHandle } from './UploadSplitButton'
import { useFilesNavigation } from './useFilesNavigation'

const ROOT: SyncRoot = {
  id:         'r-1',
  user_id:    'u-1',
  name:       'Photos',
  path:       'Photos',
  created_at: '2026-01-01T00:00:00Z',
}

function harness(overrides: Partial<Parameters<typeof useFilesNavigation>[0]> = {}) {
  const go = vi.fn()
  const createFolderOpenFor = vi.fn()
  const setPendingAction = vi.fn()
  const openFilePicker = vi.fn()
  const uploadBtnRef = { current: { openFilePicker } as unknown as UploadSplitButtonHandle }

  const { result } = renderHook(() => useFilesNavigation({
    go: go as never,
    selectedRoot:    null,
    currentPath:     '/',
    mountMode:       false,
    createFolderOpenFor,
    uploadBtnRef,
    setPendingAction,
    ...overrides,
  }))

  return { result, go, createFolderOpenFor, setPendingAction, openFilePicker }
}

describe('useFilesNavigation', () => {
  it('navigateToRoot geht zu /files', () => {
    const { result, go } = harness()
    result.current.navigateToRoot()
    expect(go).toHaveBeenCalledWith('/files')
  })

  it('goShare haengt root+path an, dir=1 nur bei Verzeichnissen', () => {
    const { result, go } = harness()
    result.current.goShare('r-1', '/docs', true)
    expect(go).toHaveBeenCalledWith('/shares?root=r-1&path=%2Fdocs&dir=1')

    result.current.goShare('r-1', '/a.txt', false)
    expect(go).toHaveBeenCalledWith('/shares?root=r-1&path=%2Fa.txt')
  })

  it('navigate ist ein No-op ohne selectedRoot', () => {
    const { result, go } = harness({ selectedRoot: null })
    result.current.navigate('/docs')
    expect(go).not.toHaveBeenCalled()
  })

  it('navigate baut eine Mount-URL wenn mountMode aktiv ist', () => {
    const { result, go } = harness({ selectedRoot: ROOT, mountMode: true })
    result.current.navigate('docs')
    expect(go).toHaveBeenCalledWith('/files/_m/r-1/docs')
  })

  it('navigate baut eine eigene-Root-URL wenn mountMode aus ist', () => {
    const { result, go } = harness({ selectedRoot: ROOT, mountMode: false })
    result.current.navigate('/docs')
    expect(go).toHaveBeenCalledWith('/files/Photos/docs')
  })

  it('enterSyncRoot navigiert zur Root-Wurzel', () => {
    const { result, go } = harness()
    result.current.enterSyncRoot(ROOT)
    expect(go).toHaveBeenCalledWith('/files/Photos')
  })

  it('triggerNewFolder oeffnet direkt wenn Ziel bereits die aktuelle Root+Pfad ist', () => {
    const { result, go, createFolderOpenFor, setPendingAction } = harness({
      selectedRoot: ROOT, currentPath: '/docs',
    })
    result.current.triggerNewFolder(ROOT, '/docs')
    expect(createFolderOpenFor).toHaveBeenCalledWith('/docs')
    expect(setPendingAction).not.toHaveBeenCalled()
    expect(go).not.toHaveBeenCalled()
  })

  it('triggerNewFolder navigiert erst und merkt die Aktion vor wenn das Ziel woanders liegt', () => {
    const { result, go, createFolderOpenFor, setPendingAction } = harness({
      selectedRoot: ROOT, currentPath: '/',
    })
    result.current.triggerNewFolder(ROOT, '/docs')
    expect(createFolderOpenFor).not.toHaveBeenCalled()
    expect(setPendingAction).toHaveBeenCalledWith({ kind: 'newFolder', rootID: 'r-1', path: '/docs' })
    expect(go).toHaveBeenCalledWith('/files/Photos/docs')
  })

  it('triggerUpload oeffnet den Datei-Picker direkt wenn Ziel bereits offen ist', () => {
    const { result, go, openFilePicker, setPendingAction } = harness({
      selectedRoot: ROOT, currentPath: '/docs',
    })
    result.current.triggerUpload(ROOT, '/docs')
    expect(openFilePicker).toHaveBeenCalled()
    expect(setPendingAction).not.toHaveBeenCalled()
    expect(go).not.toHaveBeenCalled()
  })

  it('triggerUpload navigiert erst und merkt die Aktion vor wenn das Ziel woanders liegt', () => {
    const { result, go, openFilePicker, setPendingAction } = harness({
      selectedRoot: ROOT, currentPath: '/',
    })
    result.current.triggerUpload(ROOT, '/docs')
    expect(openFilePicker).not.toHaveBeenCalled()
    expect(setPendingAction).toHaveBeenCalledWith({ kind: 'upload', rootID: 'r-1', path: '/docs' })
    expect(go).toHaveBeenCalledWith('/files/Photos/docs')
  })
})
