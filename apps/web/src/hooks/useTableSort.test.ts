import { describe, expect, it, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTableSort, SORT_STORAGE_PREFIX } from './useTableSort'

type Row = { name: string; size: number }
const rows: Row[] = [
  { name: 'b', size: 2 },
  { name: 'a', size: 3 },
  { name: 'c', size: 1 },
]
const comparators = {
  name: (a: Row, b: Row) => a.name.localeCompare(b.name),
  size: (a: Row, b: Row) => a.size - b.size,
}

beforeEach(() => {
  localStorage.clear()
})

describe('useTableSort persistence', () => {
  it('initial state is null when no storage entry', () => {
    const { result } = renderHook(() =>
      useTableSort(rows, { storageKey: 'k1', comparators }),
    )
    expect(result.current.sortKey).toBeNull()
    expect(result.current.sortDir).toBeNull()
  })

  it('loads persisted sort state from localStorage', () => {
    localStorage.setItem(
      `${SORT_STORAGE_PREFIX}k1`,
      JSON.stringify({ key: 'size', dir: 'desc' }),
    )
    const { result } = renderHook(() =>
      useTableSort(rows, { storageKey: 'k1', comparators }),
    )
    expect(result.current.sortKey).toBe('size')
    expect(result.current.sortDir).toBe('desc')
  })

  it('persists sort state on toggleSort', () => {
    const { result } = renderHook(() =>
      useTableSort(rows, { storageKey: 'k2', comparators }),
    )
    act(() => result.current.toggleSort('name'))
    expect(localStorage.getItem(`${SORT_STORAGE_PREFIX}k2`)).toBe(
      JSON.stringify({ key: 'name', dir: 'asc' }),
    )
  })

  it('ignores malformed JSON in storage', () => {
    localStorage.setItem(`${SORT_STORAGE_PREFIX}bad`, 'not-json{')
    const { result } = renderHook(() =>
      useTableSort(rows, { storageKey: 'bad', comparators }),
    )
    expect(result.current.sortKey).toBeNull()
  })

  it('ignores persisted key that is not in comparators', () => {
    localStorage.setItem(
      `${SORT_STORAGE_PREFIX}ghost`,
      JSON.stringify({ key: 'unknown', dir: 'asc' }),
    )
    const { result } = renderHook(() =>
      useTableSort(rows, { storageKey: 'ghost', comparators }),
    )
    expect(result.current.sortKey).toBeNull()
  })
})

describe('useTableSort toggle cycle', () => {
  it('cycles asc → desc → null on same column', () => {
    const { result } = renderHook(() =>
      useTableSort(rows, { storageKey: 'cyc', comparators }),
    )
    act(() => result.current.toggleSort('name'))
    expect(result.current.sortDir).toBe('asc')
    act(() => result.current.toggleSort('name'))
    expect(result.current.sortDir).toBe('desc')
    act(() => result.current.toggleSort('name'))
    expect(result.current.sortKey).toBeNull()
    expect(result.current.sortDir).toBeNull()
  })

  it('switching column resets to asc', () => {
    const { result } = renderHook(() =>
      useTableSort(rows, { storageKey: 'sw', comparators }),
    )
    act(() => result.current.toggleSort('name'))
    act(() => result.current.toggleSort('name'))
    act(() => result.current.toggleSort('size'))
    expect(result.current.sortKey).toBe('size')
    expect(result.current.sortDir).toBe('asc')
  })

  it('clears storage when cycled back to null', () => {
    const { result } = renderHook(() =>
      useTableSort(rows, { storageKey: 'clr', comparators }),
    )
    act(() => result.current.toggleSort('name'))
    act(() => result.current.toggleSort('name'))
    act(() => result.current.toggleSort('name'))
    expect(localStorage.getItem(`${SORT_STORAGE_PREFIX}clr`)).toBeNull()
  })
})

describe('useTableSort sorted output', () => {
  it('returns items unchanged when no sort active', () => {
    const { result } = renderHook(() =>
      useTableSort(rows, { storageKey: 'i', comparators }),
    )
    expect(result.current.sorted).toEqual(rows)
  })

  it('sorts asc by name', () => {
    const { result } = renderHook(() =>
      useTableSort(rows, { storageKey: 'a', comparators }),
    )
    act(() => result.current.toggleSort('name'))
    expect(result.current.sorted.map((r) => r.name)).toEqual(['a', 'b', 'c'])
  })

  it('sorts desc by size after second toggle', () => {
    const { result } = renderHook(() =>
      useTableSort(rows, { storageKey: 'd', comparators }),
    )
    act(() => result.current.toggleSort('size'))
    act(() => result.current.toggleSort('size'))
    expect(result.current.sorted.map((r) => r.size)).toEqual([3, 2, 1])
  })
})
