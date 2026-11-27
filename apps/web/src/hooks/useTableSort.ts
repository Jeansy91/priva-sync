import { useCallback, useEffect, useMemo, useState } from 'react'

export const SORT_STORAGE_PREFIX = 'priva-sync:tableSort:'

export type SortDir = 'asc' | 'desc'
export type SortState<K extends string> = { key: K; dir: SortDir } | null
export type Comparator<T> = (a: T, b: T) => number

export interface UseTableSortOptions<T, K extends string> {
  storageKey: string
  comparators: Record<K, Comparator<T>>
  defaultSort?: SortState<K>
}

export interface UseTableSortResult<T, K extends string> {
  sorted: T[]
  sortKey: K | null
  sortDir: SortDir | null
  toggleSort: (key: K) => void
  // setSort sets key + direction explicitly; bypasses the cycle in toggleSort.
  setSort: (key: K, dir: SortDir) => void
}

function readPersisted<K extends string>(
  storageKey: string,
  allowedKeys: readonly K[],
): SortState<K> {
  try {
    const raw = localStorage.getItem(SORT_STORAGE_PREFIX + storageKey)
    if (!raw) return null
    const parsed = JSON.parse(raw) as { key?: unknown; dir?: unknown }
    if (
      parsed &&
      typeof parsed.key === 'string' &&
      (parsed.dir === 'asc' || parsed.dir === 'desc') &&
      (allowedKeys as readonly string[]).includes(parsed.key)
    ) {
      return { key: parsed.key as K, dir: parsed.dir }
    }
    return null
  } catch {
    return null
  }
}

export function useTableSort<T, K extends string>(
  items: T[],
  opts: UseTableSortOptions<T, K>,
): UseTableSortResult<T, K> {
  const { storageKey, comparators, defaultSort = null } = opts
  const allowedKeys = useMemo(() => Object.keys(comparators) as K[], [comparators])

  const [state, setState] = useState<SortState<K>>(() => {
    const persisted = readPersisted<K>(storageKey, allowedKeys)
    return persisted ?? defaultSort
  })

  useEffect(() => {
    const k = SORT_STORAGE_PREFIX + storageKey
    if (state === null) {
      localStorage.removeItem(k)
    } else {
      localStorage.setItem(k, JSON.stringify(state))
    }
  }, [state, storageKey])

  const toggleSort = useCallback((key: K) => {
    setState((prev) => {
      if (!prev || prev.key !== key) return { key, dir: 'asc' }
      if (prev.dir === 'asc') return { key, dir: 'desc' }
      return null
    })
  }, [])

  const setSort = useCallback((key: K, dir: SortDir) => {
    setState({ key, dir })
  }, [])

  const sorted = useMemo(() => {
    if (!state) return items
    const cmp = comparators[state.key]
    if (!cmp) return items
    const copy = items.slice()
    copy.sort(cmp)
    if (state.dir === 'desc') copy.reverse()
    return copy
  }, [items, state, comparators])

  return {
    sorted,
    sortKey: state?.key ?? null,
    sortDir: state?.dir ?? null,
    toggleSort,
    setSort,
  }
}
