import { describe, expect, it } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useMultiselect } from './useMultiselect'

const paths = ['/a', '/b', '/c', '/d']

function clickEvt(opts: { ctrlKey?: boolean; metaKey?: boolean; shiftKey?: boolean } = {}) {
  return {
    ctrlKey:  opts.ctrlKey  ?? false,
    metaKey:  opts.metaKey  ?? false,
    shiftKey: opts.shiftKey ?? false,
  } as React.MouseEvent
}

describe('useMultiselect — clear / toggleAll / setSelected', () => {
  it('clear() empties selection and anchor', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.handleClick('/a', clickEvt(), paths) })
    act(() => { result.current.handleClick('/b', clickEvt({ ctrlKey: true }), paths) })
    act(() => { result.current.clear() })
    expect(result.current.selected.size).toBe(0)
    expect(result.current.anchor).toBeNull()
  })

  it('toggleAll selects all when nothing is selected', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.toggleAll(paths) })
    expect(Array.from(result.current.selected).sort()).toEqual(['/a', '/b', '/c', '/d'])
  })

  it('toggleAll selects all (not clears) when only some are selected', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.handleClick('/a', clickEvt(), paths) })
    act(() => { result.current.toggleAll(paths) })
    expect(Array.from(result.current.selected).sort()).toEqual(['/a', '/b', '/c', '/d'])
  })

  it('toggleAll clears when all selected', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.toggleAll(paths) })
    act(() => { result.current.toggleAll(paths) })
    expect(result.current.selected.size).toBe(0)
  })

  it('replaceSelection sets exact set', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.replaceSelection(new Set(['/b', '/d'])) })
    expect(Array.from(result.current.selected).sort()).toEqual(['/b', '/d'])
  })

  it('addSelection merges paths into selection', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.handleClick('/a', clickEvt(), paths) })
    act(() => { result.current.addSelection(['/c', '/d']) })
    expect(Array.from(result.current.selected).sort()).toEqual(['/a', '/c', '/d'])
  })
})

describe('useMultiselect — shift click', () => {
  it('shift-click extends from anchor to target (forward)', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.handleClick('/a', clickEvt(), paths) })
    act(() => { result.current.handleClick('/c', clickEvt({ shiftKey: true }), paths) })
    expect(Array.from(result.current.selected).sort()).toEqual(['/a', '/b', '/c'])
  })

  it('shift-click extends from anchor backwards', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.handleClick('/d', clickEvt(), paths) })
    act(() => { result.current.handleClick('/b', clickEvt({ shiftKey: true }), paths) })
    expect(Array.from(result.current.selected).sort()).toEqual(['/b', '/c', '/d'])
  })

  it('shift-click without anchor selects only target', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.handleClick('/c', clickEvt({ shiftKey: true }), paths) })
    expect(Array.from(result.current.selected)).toEqual(['/c'])
  })

  it('shift-click does not move anchor', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.handleClick('/a', clickEvt(), paths) })
    act(() => { result.current.handleClick('/c', clickEvt({ shiftKey: true }), paths) })
    expect(result.current.anchor).toBe('/a')
  })
})

describe('useMultiselect — ctrl click', () => {
  it('ctrl-click on unselected path adds it + sets anchor', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.handleClick('/a', clickEvt(), paths) })
    act(() => { result.current.handleClick('/c', clickEvt({ ctrlKey: true }), paths) })
    expect(Array.from(result.current.selected).sort()).toEqual(['/a', '/c'])
    expect(result.current.anchor).toBe('/c')
  })

  it('ctrl-click on selected path removes it', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.handleClick('/a', clickEvt(), paths) })
    act(() => { result.current.handleClick('/c', clickEvt({ ctrlKey: true }), paths) })
    act(() => { result.current.handleClick('/a', clickEvt({ ctrlKey: true }), paths) })
    expect(Array.from(result.current.selected)).toEqual(['/c'])
  })

  it('meta-key behaves like ctrl (Cmd on macOS)', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.handleClick('/a', clickEvt(), paths) })
    act(() => { result.current.handleClick('/b', clickEvt({ metaKey: true }), paths) })
    expect(Array.from(result.current.selected).sort()).toEqual(['/a', '/b'])
  })
})

describe('useMultiselect — plain click', () => {
  it('starts empty', () => {
    const { result } = renderHook(() => useMultiselect())
    expect(Array.from(result.current.selected)).toEqual([])
  })

  it('plain click replaces selection with that path + sets anchor', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.handleClick('/b', clickEvt(), paths) })
    expect(Array.from(result.current.selected)).toEqual(['/b'])
    expect(result.current.anchor).toBe('/b')
  })

  it('plain click after multiselect collapses to clicked path', () => {
    const { result } = renderHook(() => useMultiselect())
    act(() => { result.current.handleClick('/a', clickEvt(), paths) })
    act(() => { result.current.handleClick('/b', clickEvt({ ctrlKey: true }), paths) })
    act(() => { result.current.handleClick('/c', clickEvt(), paths) })
    expect(Array.from(result.current.selected)).toEqual(['/c'])
    expect(result.current.anchor).toBe('/c')
  })
})
