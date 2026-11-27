import { describe, expect, it, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useFilesDnD } from './useFilesDnD'
import { useMultiselect } from './useMultiselect'
import type { DirEntry } from './helpers'

function dirEntry(path: string): DirEntry {
  const name = path.slice(path.lastIndexOf('/') + 1)
  return { kind: 'dir', name, path }
}

// Minimal DataTransfer stand-in: only the bits the hook touches.
function fakeDataTransfer() {
  const store = new Map<string, string>()
  return {
    effectAllowed: 'none',
    dropEffect:    'none',
    types:         [] as string[],
    setData(type: string, val: string) { store.set(type, val); this.types.push(type) },
    getData(type: string) { return store.get(type) ?? '' },
  }
}

function dragEvt() {
  return {
    preventDefault:  vi.fn(),
    stopPropagation: vi.fn(),
    dataTransfer:    fakeDataTransfer(),
  } as unknown as React.DragEvent & { preventDefault: ReturnType<typeof vi.fn> }
}

// Drives a useMultiselect + useFilesDnD pair so the drag-set logic (single row
// vs. whole selection) can be exercised against real selection state.
function setup(enabled = true) {
  const moveTo = vi.fn()
  const hook = renderHook(() => {
    const multi = useMultiselect()
    const dnd = useFilesDnD({ multi, moveTo, enabled })
    return { multi, dnd }
  })
  return { ...hook, moveTo }
}

describe('useFilesDnD — drag set', () => {
  it('drags only the grabbed row when it is not part of a multi-selection', () => {
    const { result, moveTo } = setup()
    const ev = dragEvt()
    act(() => { result.current.dnd.dragProps(dirEntry('/src')).onDragStart(ev) })

    const drop = dragEvt()
    act(() => { result.current.dnd.dropProps('/dst').onDrop(drop) })
    expect(moveTo).toHaveBeenCalledWith('/dst', new Set(['dir:/src']))
  })

  it('drags the whole selection when the grabbed row is part of it', () => {
    const { result, moveTo } = setup()
    act(() => { result.current.multi.replaceSelection(new Set(['dir:/a', 'dir:/b'])) })

    const ev = dragEvt()
    act(() => { result.current.dnd.dragProps(dirEntry('/a')).onDragStart(ev) })
    const drop = dragEvt()
    act(() => { result.current.dnd.dropProps('/dst').onDrop(drop) })
    expect(moveTo).toHaveBeenCalledWith('/dst', new Set(['dir:/a', 'dir:/b']))
  })

  it('marks the drag with the internal-move MIME type', () => {
    const { result } = setup()
    const ev = dragEvt()
    act(() => { result.current.dnd.dragProps(dirEntry('/x')).onDragStart(ev) })
    expect(ev.dataTransfer.types).toContain('application/x-privasync-move')
  })

  it('is not draggable when disabled', () => {
    const { result } = setup(false)
    expect(result.current.dnd.dragProps(dirEntry('/x')).draggable).toBe(false)
  })
})

describe('useFilesDnD — drop validation', () => {
  it('accepts a drop into a sibling directory (preventDefault)', () => {
    const { result } = setup()
    act(() => { result.current.dnd.dragProps(dirEntry('/a/src')).onDragStart(dragEvt()) })
    const over = dragEvt()
    act(() => { result.current.dnd.dropProps('/a/other').onDragOver(over) })
    expect(over.preventDefault).toHaveBeenCalled()
  })

  it('rejects dropping a folder into itself', () => {
    const { result } = setup()
    act(() => { result.current.dnd.dragProps(dirEntry('/a/src')).onDragStart(dragEvt()) })
    const over = dragEvt()
    act(() => { result.current.dnd.dropProps('/a/src').onDragOver(over) })
    expect(over.preventDefault).not.toHaveBeenCalled()
  })

  it('rejects dropping a folder into its own descendant', () => {
    const { result } = setup()
    act(() => { result.current.dnd.dragProps(dirEntry('/a/src')).onDragStart(dragEvt()) })
    const over = dragEvt()
    act(() => { result.current.dnd.dropProps('/a/src/nested').onDragOver(over) })
    expect(over.preventDefault).not.toHaveBeenCalled()
  })

  it('rejects a no-op drop into the current parent directory', () => {
    const { result } = setup()
    act(() => { result.current.dnd.dragProps(dirEntry('/a/src')).onDragStart(dragEvt()) })
    const over = dragEvt()
    act(() => { result.current.dnd.dropProps('/a').onDragOver(over) })
    expect(over.preventDefault).not.toHaveBeenCalled()
  })

  it('does not call moveTo when the drop target is invalid', () => {
    const { result, moveTo } = setup()
    act(() => { result.current.dnd.dragProps(dirEntry('/a/src')).onDragStart(dragEvt()) })
    act(() => { result.current.dnd.dropProps('/a/src').onDrop(dragEvt()) })
    expect(moveTo).not.toHaveBeenCalled()
  })
})

describe('useFilesDnD — hover highlight', () => {
  it('isDropActive tracks the hovered valid target', () => {
    const { result } = setup()
    act(() => { result.current.dnd.dragProps(dirEntry('/src')).onDragStart(dragEvt()) })
    act(() => { result.current.dnd.dropProps('/dst').onDragOver(dragEvt()) })
    expect(result.current.dnd.isDropActive('/dst')).toBe(true)
    expect(result.current.dnd.isDropActive('/other')).toBe(false)
  })

  it('clears the highlight after the drop', () => {
    const { result } = setup()
    act(() => { result.current.dnd.dragProps(dirEntry('/src')).onDragStart(dragEvt()) })
    act(() => { result.current.dnd.dropProps('/dst').onDragOver(dragEvt()) })
    act(() => { result.current.dnd.dropProps('/dst').onDrop(dragEvt()) })
    expect(result.current.dnd.isDropActive('/dst')).toBe(false)
  })
})
